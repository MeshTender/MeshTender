// Package mesh wraps the meshcore-go SDK to build and decode the MeshCore
// packets MeshTender exchanges with a repeater: an anonymous (pubkey-ACL)
// login request and its response. All crypto happens here on the server; the
// raw packet bytes produced/consumed travel over the wire via a KISS modem.
package mesh

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// respLoginOK is the firmware's RESP_SERVER_LOGIN_OK code (offset 4 of the
// decrypted login response).
const respLoginOK byte = 0x00

// LoginResponse is the decoded repeater reply to a login request. A non-nil
// LoginResponse means the reply was addressed to us and its MAC verified under
// our shared secret with the repeater — i.e. we provably reached the repeater
// and it holds the matching private key.
type LoginResponse struct {
	ServerTime  time.Time
	LoginOK     bool
	IsAdmin     bool
	Permissions byte
	Plaintext   []byte // full decrypted payload, for diagnostics
	// FromPath is true when the reply was a PATH packet (vs a direct RESPONSE).
	FromPath bool
}

// BuildLoginPacket builds the raw MeshCore LoRa packet bytes that perform an
// anonymous login to repeater using server's identity. password may be empty
// when access was granted via `setperm <pubkey> 3` (pubkey ACL). now is the
// timestamp embedded in the request (the repeater uses it for clock sync).
func BuildLoginPacket(server meshcore.LocalIdentity, repeater meshcore.Identity, password string, now time.Time) ([]byte, error) {
	shared, err := server.SharedSecret(repeater)
	if err != nil {
		return nil, fmt.Errorf("derive shared secret: %w", err)
	}

	// Repeater login request plaintext: timestamp(4, little-endian) + password.
	plaintext := make([]byte, 4+len(password))
	binary.LittleEndian.PutUint32(plaintext[:4], uint32(now.Unix()))
	copy(plaintext[4:], password)

	enc, err := meshcore.EncryptThenMAC(shared, plaintext) // MAC(2) || ciphertext
	if err != nil {
		return nil, fmt.Errorf("encrypt login: %w", err)
	}

	anon := &meshcore.AnonReq{
		Destination:      repeater.Hash()[0],
		MAC:              [2]byte{enc[0], enc[1]},
		EncryptedPayload: enc[2:],
	}
	copy(anon.EphemeralPubKey[:], server.PublicKeyBytes())

	payload, err := anon.ToBytes()
	if err != nil {
		return nil, fmt.Errorf("encode anon req: %w", err)
	}

	pkt := &meshcore.Packet{
		Header:  meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeAnonReq, 0),
		Payload: payload,
	}
	raw, err := pkt.ToBytes()
	if err != nil {
		return nil, fmt.Errorf("encode packet: %w", err)
	}
	return raw, nil
}

// ErrNotForUs indicates the packet was a valid RESPONSE but not addressed to
// the server identity (e.g. overheard mesh traffic). Callers should keep
// listening for the matching reply.
var ErrNotForUs = errors.New("mesh: response not addressed to server identity")

// DecodeLoginResponse parses a raw received LoRa packet and, if it is a reply
// addressed to server whose MAC verifies under the shared secret with repeater,
// returns the decoded login result. Packets that are not replies for us return
// ErrNotForUs so the caller can keep waiting.
//
// A repeater answers a flood login with either a RESPONSE (when a path already
// exists) or, more commonly on first contact, a PATH packet that carries the
// return route plus the response as trailing "extra" data. Both share the same
// [dest][src][MAC][ciphertext] layout and the same ECDH shared secret; for a
// PATH reply we strip the leading [path_len][path…] before reading the response.
func DecodeLoginResponse(server meshcore.LocalIdentity, repeater meshcore.Identity, raw []byte) (*LoginResponse, error) {
	plain, payloadType, err := decodeAddressedReply(server, repeater, raw)
	if err != nil {
		return nil, err
	}

	// A flooded login is answered with a PATH return packet whose decrypted
	// content wraps the response; a direct reply (RESPONSE) carries it as-is.
	body := plain
	isPath := payloadType == meshcore.PayloadTypePath
	switch payloadType {
	case meshcore.PayloadTypeResponse:
		// body already the response
	case meshcore.PayloadTypePath:
		extra, ok := unwrapPathReturn(plain)
		if !ok {
			return nil, ErrNotForUs // malformed/unexpected path wrapper
		}
		body = extra
	default:
		return nil, ErrNotForUs
	}

	lr := parseLoginPayload(body)
	lr.FromPath = isPath
	return lr, nil
}

// decodeAddressedReply parses a received packet that should be a reply addressed
// to the server, verifies its MAC under the shared secret with repeater, and
// returns the decrypted plaintext plus the packet's payload type. All reply
// envelopes (RESPONSE, PATH, TXT_MSG) share the [dest][src][MAC][ciphertext]
// layout. Returns ErrNotForUs when the packet isn't a MAC-verified reply to us.
func decodeAddressedReply(server meshcore.LocalIdentity, repeater meshcore.Identity, raw []byte) (plain []byte, payloadType byte, err error) {
	pkt, err := meshcore.PacketFromBytes(raw)
	if err != nil {
		return nil, 0, fmt.Errorf("parse packet: %w", err)
	}
	payloadType = pkt.PayloadType()

	p := pkt.Payload
	if len(p) < 4 { // [dest][src][MAC(2)] minimum
		return nil, payloadType, ErrNotForUs
	}
	dest := p[0]
	mac := []byte{p[2], p[3]}
	ciphertext := p[4:]

	// Destination of the reply is the hash of our (server) public key.
	if dest != server.Identity.Hash()[0] {
		return nil, payloadType, ErrNotForUs
	}

	shared, err := server.SharedSecret(repeater)
	if err != nil {
		return nil, payloadType, fmt.Errorf("derive shared secret: %w", err)
	}
	// MACThenDecrypt verifies the 2-byte MAC and decrypts in one step; nil means
	// the MAC failed — either not really for us (1-byte hash collision) or we
	// don't hold the matching key.
	dec, _ := meshcore.MACThenDecrypt(shared, append(mac, ciphertext...))
	if dec == nil {
		return nil, payloadType, ErrNotForUs
	}
	return dec, payloadType, nil
}

// unwrapPathReturn extracts the embedded payload from a PATH return packet's
// decrypted content. Per the repeater firmware (Mesh::createPathReturn) the
// plaintext is laid out as:
//
//	[path_len:1][path: count*size bytes][extra_type:1][extra…]
//
// where count = path_len & 0x3f and size = (path_len>>6)+1. It returns the
// extra payload, and false if the framing is malformed or the embedded type is
// not a RESPONSE.
func unwrapPathReturn(plain []byte) (extra []byte, ok bool) {
	if len(plain) < 1 {
		return nil, false
	}
	pathLen := plain[0]
	pathBytes := int(pathLen&0x3f) * int((pathLen>>6)+1)
	typeOff := 1 + pathBytes
	if typeOff >= len(plain) {
		return nil, false
	}
	if plain[typeOff] != meshcore.PayloadTypeResponse {
		return nil, false
	}
	return plain[typeOff+1:], true
}

// Field offsets within the repeater's login RESPONSE payload (reply_data), per
// examples/simple_repeater/MyMesh.cpp handleLoginReq:
//
//	[0:4] server timestamp (LE) · [4] result code · [5] legacy keep-alive ·
//	[6] admin flag (1/0) · [7] permissions · [8:12] random · [12] firmware level
const (
	respCodeOffset  = 4
	respAdminOffset = 6
	respPermsOffset = 7
)

// parseLoginPayload reads the fixed-layout login RESPONSE fields. Only a
// MAC-verified reply is needed to consider the repeater reachable; these fields
// are informational and tolerate a short payload.
func parseLoginPayload(d []byte) *LoginResponse {
	lr := &LoginResponse{Plaintext: d}
	if len(d) >= 4 {
		lr.ServerTime = time.Unix(int64(binary.LittleEndian.Uint32(d[:4])), 0).UTC()
	}
	if len(d) > respCodeOffset {
		lr.LoginOK = d[respCodeOffset] == respLoginOK
	}
	if len(d) > respAdminOffset {
		lr.IsAdmin = d[respAdminOffset] == 1
	}
	if len(d) > respPermsOffset {
		lr.Permissions = d[respPermsOffset]
	}
	return lr
}
