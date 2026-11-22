// Package relay sends MeshCore packets onto the mesh through an observer, for
// sessions that have no KISS modem. MeshTender builds and signs each packet as
// it would for a modem, wraps it in a TX frame signed with the server's key, and
// publishes the frame to the observer's tx topic on a broker they share. The
// observer checks the frame and transmits the packet unchanged; replies come
// back on the observer's normal packets uplink.
//
// The frame format and topics are protocol v1, the contract with MeshTender's
// observer firmware (src/helpers/MeshTenderTx.h in MeshTender/MeshCore). Change
// them only together with the firmware, by bumping Version.
package relay

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// Version is the TX frame protocol version.
const Version = 1

// MaxPacketLen is the longest MeshCore packet a frame can carry
// (MAX_TRANS_UNIT).
const MaxPacketLen = 255

// Frame layout (little-endian):
//
//	0     1   version
//	1     1   flags, always 0
//	2     4   request id, echoed in the result
//	6     4   timestamp, Unix seconds
//	10    32  signer public key
//	42    1   packet length N
//	43    N   raw packet
//	43+N  64  Ed25519 signature over "MTTX" + target public key + frame[0:43+N]
const (
	headerLen = 43
	sigLen    = 64
)

// signDomain separates TX frame signatures from anything else the server key
// signs.
const signDomain = "MTTX"

// ErrPacketSize is returned for an empty packet or one longer than MaxPacketLen.
var ErrPacketSize = errors.New("relay: packet must be 1 to 255 bytes")

// EncodeTxFrame wraps packet in a TX frame for the observer target, signed by
// signer. The signature covers the target's key, so the frame is only accepted
// by that observer.
func EncodeTxFrame(target meshcore.Identity, signer meshcore.LocalIdentity, requestID uint32, ts time.Time, packet []byte) ([]byte, error) {
	if len(packet) == 0 || len(packet) > MaxPacketLen {
		return nil, ErrPacketSize
	}
	unix := ts.Unix()
	if unix < 0 || unix > 1<<32-1 {
		return nil, fmt.Errorf("relay: timestamp %v out of range", ts)
	}
	frame := make([]byte, 0, headerLen+len(packet)+sigLen)
	frame = append(frame, Version, 0)
	frame = binary.LittleEndian.AppendUint32(frame, requestID)
	frame = binary.LittleEndian.AppendUint32(frame, uint32(unix))
	frame = append(frame, signer.PublicKeyBytes()...)
	frame = append(frame, byte(len(packet))) //nolint:gosec // G115: checked to be at most MaxPacketLen above
	frame = append(frame, packet...)
	return append(frame, signer.Sign(signedBytes(target, frame))...), nil
}

// signedBytes is what a frame's signature covers: the domain, the target
// observer's key, then the frame up to the signature.
func signedBytes(target meshcore.Identity, body []byte) []byte {
	msg := make([]byte, 0, len(signDomain)+meshcore.PubKeySize+len(body))
	msg = append(msg, signDomain...)
	msg = append(msg, target.PublicKeyBytes()...)
	return append(msg, body...)
}

// Result is the observer's answer to one frame, published on its txresult
// topic. OK means the packet was queued for transmit, not that it was heard.
type Result struct {
	Version   int    `json:"v"`
	RequestID uint32 `json:"id"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

// The error codes an observer reports in a Result.
const (
	ErrCodeBadFrame           = "bad_frame"
	ErrCodeUnsupportedVersion = "unsupported_version"
	ErrCodeClockUnsynced      = "clock_unsynced"
	ErrCodeStale              = "stale"
	ErrCodeNotAdmin           = "not_admin"
	ErrCodeBadSignature       = "bad_signature"
	ErrCodeReplay             = "replay"
	ErrCodeBusy               = "busy"
	ErrCodeBadPacket          = "bad_packet"
)

// ParseResult decodes a txresult payload.
func ParseResult(payload []byte) (Result, error) {
	var r Result
	if err := json.Unmarshal(payload, &r); err != nil {
		return Result{}, fmt.Errorf("relay: result: %w", err)
	}
	if r.Version != Version {
		return Result{}, fmt.Errorf("relay: result version %d", r.Version)
	}
	return r, nil
}

// observerTopicKey is how the firmware writes its key in topics: uppercase hex
// (mesh::Utils::toHex). MQTT topics are case-sensitive, so ours must match.
func observerTopicKey(observer meshcore.Identity) string {
	return strings.ToUpper(observer.String())
}

// TxTopic is where MeshTender publishes frames for observer.
func TxTopic(observer meshcore.Identity) string {
	return "meshtender/v1/" + observerTopicKey(observer) + "/tx"
}

// ResultTopic is where observer reports the outcome of each frame.
func ResultTopic(observer meshcore.Identity) string {
	return "meshtender/v1/" + observerTopicKey(observer) + "/txresult"
}

// PacketsTopic matches observer's received-packet uplink under prefix, for any
// IATA.
func PacketsTopic(prefix string, observer meshcore.Identity) string {
	return prefix + "/+/" + observerTopicKey(observer) + "/packets"
}
