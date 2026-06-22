package mesh

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

func mustIdentity(t *testing.T) meshcore.LocalIdentity {
	t.Helper()
	id, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	return id
}

// buildRepeaterResponse simulates what a real repeater firmware emits in reply
// to a login: a RESPONSE packet addressed to the server, encrypted under the
// repeater↔server shared secret.
func buildRepeaterResponse(t *testing.T, repeater meshcore.LocalIdentity, server meshcore.Identity, admin bool, perms byte, now time.Time) []byte {
	t.Helper()
	shared, err := repeater.SharedSecret(server)
	if err != nil {
		t.Fatalf("repeater shared secret: %v", err)
	}

	plain := make([]byte, 13)
	binary.LittleEndian.PutUint32(plain[:4], uint32(now.Unix()))
	plain[4] = respLoginOK
	plain[5] = 0 // reserved
	if admin {
		plain[6] = 1
	}
	plain[7] = perms
	// plain[8:12] random blob, plain[12] firmware level — left zero.

	enc, err := meshcore.EncryptThenMAC(shared, plain)
	if err != nil {
		t.Fatalf("encrypt response: %v", err)
	}
	resp := &meshcore.Response{
		Destination:      server.Hash()[0],
		Source:           repeater.Identity.Hash()[0],
		MAC:              [2]byte{enc[0], enc[1]},
		EncryptedPayload: enc[2:],
	}
	payload, err := resp.ToBytes()
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	pkt := &meshcore.Packet{
		Header:  meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeResponse, 0),
		Payload: payload,
	}
	raw, err := pkt.ToBytes()
	if err != nil {
		t.Fatalf("encode packet: %v", err)
	}
	return raw
}

func TestLoginRequestDecodableByRepeater(t *testing.T) {
	t.Parallel()
	server := mustIdentity(t)
	repeater := mustIdentity(t)
	now := time.Unix(1_700_000_000, 0)

	raw, err := BuildLoginPacket(server, repeater.Identity, "hunter2", now)
	if err != nil {
		t.Fatalf("BuildLoginPacket: %v", err)
	}

	// Repeater-side decode.
	pkt, err := meshcore.PacketFromBytes(raw)
	if err != nil {
		t.Fatalf("PacketFromBytes: %v", err)
	}
	if pkt.PayloadType() != meshcore.PayloadTypeAnonReq {
		t.Fatalf("payload type = %d, want AnonReq", pkt.PayloadType())
	}
	if pkt.PathHashSize() != 3 {
		t.Errorf("path hash size = %d, want 3", pkt.PathHashSize())
	}
	if pkt.PathHashCount() != 0 {
		t.Errorf("fresh flood path hash count = %d, want 0", pkt.PathHashCount())
	}
	anon, err := meshcore.AnonReqFromBytes(pkt.Payload)
	if err != nil {
		t.Fatalf("AnonReqFromBytes: %v", err)
	}
	if anon.Destination != repeater.Identity.Hash()[0] {
		t.Errorf("destination = 0x%02x, want 0x%02x", anon.Destination, repeater.Identity.Hash()[0])
	}
	// The repeater derives the shared secret from the sender pubkey in the packet.
	sender, err := meshcore.NewIdentityFromBytes(anon.EphemeralPubKey[:])
	if err != nil {
		t.Fatalf("sender identity: %v", err)
	}
	shared, err := repeater.SharedSecret(sender)
	if err != nil {
		t.Fatalf("repeater shared secret: %v", err)
	}
	if !anon.VerifyMAC(shared) {
		t.Fatal("repeater could not verify MAC of login request")
	}
	plain := anon.Decrypt(shared)
	if plain == nil {
		t.Fatal("repeater decrypt returned nil")
	}
	if got := binary.LittleEndian.Uint32(plain[:4]); got != uint32(now.Unix()) {
		t.Errorf("timestamp = %d, want %d", got, now.Unix())
	}
	// Password follows the timestamp (zero-padded to a 16-byte block).
	if got := string(plain[4:11]); got != "hunter2" {
		t.Errorf("password = %q, want %q", got, "hunter2")
	}
}

// buildRepeaterPathReply simulates a repeater answering a flood login with a
// PATH packet: the response data follows a [path_len][path…] prefix.
func buildRepeaterPathReply(t *testing.T, repeater meshcore.LocalIdentity, server meshcore.Identity, path []byte, admin bool, perms byte, now time.Time) []byte {
	t.Helper()
	shared, err := repeater.SharedSecret(server)
	if err != nil {
		t.Fatalf("repeater shared secret: %v", err)
	}

	resp := make([]byte, 13)
	binary.LittleEndian.PutUint32(resp[:4], uint32(now.Unix()))
	resp[4] = respLoginOK
	if admin {
		resp[6] = 1
	}
	resp[7] = perms

	// Firmware PATH framing: [path_len][path][extra_type=RESPONSE][reply_data].
	plain := append([]byte{byte(len(path))}, path...)
	plain = append(plain, meshcore.PayloadTypeResponse)
	plain = append(plain, resp...)

	enc, err := meshcore.EncryptThenMAC(shared, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	p := &meshcore.Path{
		Destination:      server.Hash()[0],
		Source:           repeater.Identity.Hash()[0],
		MAC:              [2]byte{enc[0], enc[1]},
		EncryptedPayload: enc[2:],
	}
	payload, err := p.ToBytes()
	if err != nil {
		t.Fatalf("path bytes: %v", err)
	}
	pkt := &meshcore.Packet{
		Header:  meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypePath, 0),
		Payload: payload,
	}
	raw, err := pkt.ToBytes()
	if err != nil {
		t.Fatalf("packet bytes: %v", err)
	}
	return raw
}

func TestLoginPathReplyRoundTrip(t *testing.T) {
	t.Parallel()
	server := mustIdentity(t)
	repeater := mustIdentity(t)
	now := time.Unix(1_700_000_900, 0).UTC()

	for _, path := range [][]byte{nil, {0xAB}, {0xAB, 0xCD}} {
		raw := buildRepeaterPathReply(t, repeater, server.Identity, path, true, 3, now)
		lr, err := DecodeLoginResponse(server, repeater.Identity, raw)
		if err != nil {
			t.Fatalf("path len %d: DecodeLoginResponse: %v", len(path), err)
		}
		if !lr.LoginOK || !lr.IsAdmin || lr.Permissions != 3 {
			t.Errorf("path len %d: got LoginOK=%v admin=%v perms=%d", len(path), lr.LoginOK, lr.IsAdmin, lr.Permissions)
		}
		if !lr.ServerTime.Equal(now) {
			t.Errorf("path len %d: ServerTime = %v, want %v", len(path), lr.ServerTime, now)
		}
	}
}

// TestParseRealPathReply uses an actual decrypted PATH-reply payload captured
// from a live repeater (admin granted via `setperm <pubkey> 3`). It guards the
// timestamp-anchored field offsets against regressions.
func TestParseRealPathReply(t *testing.T) {
	t.Parallel()
	// raw = [path_len=00][tag=01][ts:4 LE = 0x6a34b5e3][code=00][rsv=00]
	//       [admin=01][perms=03][random:4][fw=02][pad=00]
	plain, err := hex.DecodeString("0001e3b5346a000001030b9213f80200")
	if err != nil {
		t.Fatal(err)
	}
	path, _, extra, ok := unwrapPathReturn(plain)
	if !ok {
		t.Fatal("unwrapPathReturn failed on real capture")
	}
	if len(path) != 0 {
		t.Errorf("path len = %d, want 0 (path_len byte was 0x00)", len(path))
	}
	lr := parseLoginPayload(extra)
	if !lr.LoginOK {
		t.Error("LoginOK = false, want true")
	}
	if !lr.IsAdmin {
		t.Error("IsAdmin = false, want true (setperm granted admin)")
	}
	if lr.Permissions != 3 {
		t.Errorf("Permissions = %d, want 3", lr.Permissions)
	}
	if got := lr.ServerTime.Unix(); got != 0x6a34b5e3 {
		t.Errorf("ServerTime = %d, want %d", got, 0x6a34b5e3)
	}
}

func TestLoginResponseRoundTrip(t *testing.T) {
	t.Parallel()
	server := mustIdentity(t)
	repeater := mustIdentity(t)
	now := time.Unix(1_700_000_500, 0).UTC()

	raw := buildRepeaterResponse(t, repeater, server.Identity, true, 3, now)

	lr, err := DecodeLoginResponse(server, repeater.Identity, raw)
	if err != nil {
		t.Fatalf("DecodeLoginResponse: %v", err)
	}
	if !lr.LoginOK {
		t.Error("LoginOK = false, want true")
	}
	if !lr.IsAdmin {
		t.Error("IsAdmin = false, want true")
	}
	if lr.Permissions != 3 {
		t.Errorf("Permissions = %d, want 3", lr.Permissions)
	}
	if !lr.ServerTime.Equal(now) {
		t.Errorf("ServerTime = %v, want %v", lr.ServerTime, now)
	}
}

func TestDecodeLoginResponseWrongRepeater(t *testing.T) {
	t.Parallel()
	server := mustIdentity(t)
	repeater := mustIdentity(t)
	other := mustIdentity(t)
	now := time.Unix(1_700_000_500, 0)

	// A genuine repeater reply, but we try to decode it as if from `other`.
	raw := buildRepeaterResponse(t, repeater, server.Identity, true, 3, now)

	_, err := DecodeLoginResponse(server, other.Identity, raw)
	if !errors.Is(err, ErrNotForUs) {
		t.Fatalf("expected ErrNotForUs for wrong repeater, got %v", err)
	}
}

func TestDecodeLoginResponseNotResponse(t *testing.T) {
	t.Parallel()
	server := mustIdentity(t)
	repeater := mustIdentity(t)

	// Feed a login request packet (AnonReq) into the response decoder.
	raw, err := BuildLoginPacket(server, repeater.Identity, "", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeLoginResponse(server, repeater.Identity, raw); !errors.Is(err, ErrNotForUs) {
		t.Fatalf("expected ErrNotForUs for non-response, got %v", err)
	}
}
