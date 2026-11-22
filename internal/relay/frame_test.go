package relay

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

var update = flag.Bool("update", false, "rewrite testdata/tx_v1_vectors.json")

// seededIdentity derives a fixed identity from a label, so the golden vectors
// are stable without committing a private key that looks like a real one.
func seededIdentity(label string) meshcore.LocalIdentity {
	return meshcore.NewLocalIdentityFromSeed(sha256.Sum256([]byte("meshtender relay test vector: " + label)))
}

func TestEncodeTxFrameLayout(t *testing.T) {
	signer, target := seededIdentity("signer"), seededIdentity("target")
	packet := []byte{0x01, 0x02, 0x03}
	ts := time.Unix(1_800_000_000, 0)
	frame, err := EncodeTxFrame(target.Identity, signer, 0xA1B2C3D4, ts, packet)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(frame), headerLen+len(packet)+sigLen; got != want {
		t.Fatalf("len = %d, want %d", got, want)
	}
	if frame[0] != Version || frame[1] != 0 {
		t.Errorf("version/flags = %x %x", frame[0], frame[1])
	}
	if id := binary.LittleEndian.Uint32(frame[2:]); id != 0xA1B2C3D4 {
		t.Errorf("request id = %x", id)
	}
	if got := binary.LittleEndian.Uint32(frame[6:]); got != 1_800_000_000 {
		t.Errorf("timestamp = %d", got)
	}
	if !bytes.Equal(frame[10:42], signer.PublicKeyBytes()) {
		t.Error("signer key not at offset 10")
	}
	if frame[42] != byte(len(packet)) || !bytes.Equal(frame[43:46], packet) {
		t.Error("packet length or bytes wrong")
	}
}

func TestEncodeTxFrameBindsTarget(t *testing.T) {
	signer, target, other := seededIdentity("signer"), seededIdentity("target"), seededIdentity("other")
	frame, err := EncodeTxFrame(target.Identity, signer, 1, time.Unix(1_800_000_000, 0), []byte{0xAA})
	if err != nil {
		t.Fatal(err)
	}
	body, sig := frame[:len(frame)-sigLen], frame[len(frame)-sigLen:]
	if !signer.Verify(signedBytes(target.Identity, body), sig) {
		t.Fatal("signature doesn't verify for its target")
	}
	if signer.Verify(signedBytes(other.Identity, body), sig) {
		t.Fatal("signature verifies for a different observer")
	}
}

func TestEncodeTxFramePacketSize(t *testing.T) {
	signer, target := seededIdentity("signer"), seededIdentity("target")
	ts := time.Unix(1_800_000_000, 0)
	for _, n := range []int{0, MaxPacketLen + 1} {
		if _, err := EncodeTxFrame(target.Identity, signer, 1, ts, make([]byte, n)); !errors.Is(err, ErrPacketSize) {
			t.Errorf("packet of %d bytes: err = %v, want ErrPacketSize", n, err)
		}
	}
	for _, n := range []int{1, MaxPacketLen} {
		frame, err := EncodeTxFrame(target.Identity, signer, 1, ts, make([]byte, n))
		if err != nil {
			t.Fatalf("packet of %d bytes: %v", n, err)
		}
		if len(frame) != headerLen+n+sigLen {
			t.Errorf("packet of %d bytes: frame is %d bytes", n, len(frame))
		}
	}
}

func TestEncodeTxFrameTimestampRange(t *testing.T) {
	signer, target := seededIdentity("signer"), seededIdentity("target")
	for _, ts := range []time.Time{time.Unix(-1, 0), time.Unix(1<<32, 0)} {
		if _, err := EncodeTxFrame(target.Identity, signer, 1, ts, []byte{1}); err == nil {
			t.Errorf("timestamp %d accepted", ts.Unix())
		}
	}
}

func TestParseResult(t *testing.T) {
	ok, err := ParseResult([]byte(`{"v":1,"id":4294967295,"ok":true}`))
	if err != nil || !ok.OK || ok.RequestID != 4294967295 || ok.Error != "" {
		t.Errorf("ok result = %+v, %v", ok, err)
	}
	bad, err := ParseResult([]byte(`{"v":1,"id":7,"ok":false,"error":"not_admin"}`))
	if err != nil || bad.OK || bad.RequestID != 7 || bad.Error != ErrCodeNotAdmin {
		t.Errorf("error result = %+v, %v", bad, err)
	}
	for _, p := range []string{`{"v":2,"id":1,"ok":true}`, `{"id":1,"ok":true}`, `not json`} {
		if _, err := ParseResult([]byte(p)); err == nil {
			t.Errorf("%s: accepted", p)
		}
	}
}

func TestTopicsUseUppercaseKey(t *testing.T) {
	obs := seededIdentity("target").Identity
	key := hex.EncodeToString(obs.PublicKeyBytes())
	upper := bytes.ToUpper([]byte(key))
	if got, want := TxTopic(obs), "meshtender/v1/"+string(upper)+"/tx"; got != want {
		t.Errorf("TxTopic = %q, want %q", got, want)
	}
	if got, want := ResultTopic(obs), "meshtender/v1/"+string(upper)+"/txresult"; got != want {
		t.Errorf("ResultTopic = %q, want %q", got, want)
	}
	if got, want := PacketsTopic("meshcore", obs), "meshcore/+/"+string(upper)+"/packets"; got != want {
		t.Errorf("PacketsTopic = %q, want %q", got, want)
	}
}

// vector is one golden TX frame, shared with the firmware's host tests
// (test/test_meshtender_tx in MeshTender/MeshCore) as a cross-language check.
type vector struct {
	Name        string `json:"name"`
	SignerSeed  string `json:"signer_seed"`
	SignerKey   string `json:"signer_public_key"`
	TargetKey   string `json:"target_public_key"`
	RequestID   uint32 `json:"request_id"`
	Timestamp   uint32 `json:"timestamp"`
	Packet      string `json:"packet"`
	Frame       string `json:"frame"`
	SignedBytes string `json:"signed_bytes"`
}

func goldenVectors(t *testing.T) []vector {
	t.Helper()
	signer, target := seededIdentity("signer"), seededIdentity("target")
	seed := signer.Seed()
	cases := []struct {
		name   string
		id     uint32
		ts     uint32
		packet []byte
	}{
		{"min_packet", 1, 1_800_000_000, []byte{0x11}},
		// A flood REQ: header 0x01 (route FLOOD, type REQ), path length 0.
		{"flood_req", 0x01020304, 1_800_000_060, append([]byte{0x01, 0x00}, bytes.Repeat([]byte{0x5A}, 20)...)},
		// A direct TXT_MSG through two 1-byte hops: header 0x0A, path 0x02 0xAB 0xCD.
		{"direct_txt", 0xFFFFFFFF, 1_800_000_120, append([]byte{0x0A, 0x02, 0xAB, 0xCD}, bytes.Repeat([]byte{0x33}, 30)...)},
		{"max_packet", 42, 1_800_000_180, bytes.Repeat([]byte{0xEE}, MaxPacketLen)},
	}
	var out []vector
	for _, c := range cases {
		frame, err := EncodeTxFrame(target.Identity, signer, c.id, time.Unix(int64(c.ts), 0), c.packet)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, vector{
			Name:        c.name,
			SignerSeed:  hex.EncodeToString(seed[:]),
			SignerKey:   signer.String(),
			TargetKey:   target.String(),
			RequestID:   c.id,
			Timestamp:   c.ts,
			Packet:      hex.EncodeToString(c.packet),
			Frame:       hex.EncodeToString(frame),
			SignedBytes: hex.EncodeToString(signedBytes(target.Identity, frame[:len(frame)-sigLen])),
		})
	}
	return out
}

func TestGoldenVectors(t *testing.T) {
	path := filepath.Join("testdata", "tx_v1_vectors.json")
	got, err := json.MarshalIndent(goldenVectors(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // fixed test path
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is out of date; run go test ./internal/relay -run TestGoldenVectors -update", path)
	}
}
