package mesh

import (
	"bytes"
	"crypto/aes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// firmwareGroupPayload builds a GRP_TXT payload the way the firmware does,
// from the standard library alone (Mesh::createGroupDatagram,
// Utils::encryptThenMAC): AES-128-ECB over the zero-padded plaintext, then a
// 2-byte HMAC-SHA256 of the ciphertext keyed with the 32-byte secret (the PSK
// zero-extended, GroupChannel::secret[PUB_KEY_SIZE]).
func firmwareGroupPayload(t *testing.T, psk [16]byte, ts uint32, flags byte, text string) []byte {
	t.Helper()
	plain := binary.LittleEndian.AppendUint32(nil, ts)
	plain = append(plain, flags)
	plain = append(plain, text...)
	for len(plain)%16 != 0 {
		plain = append(plain, 0)
	}
	block, err := aes.NewCipher(psk[:])
	if err != nil {
		t.Fatal(err)
	}
	enc := make([]byte, len(plain))
	for i := 0; i < len(plain); i += 16 {
		block.Encrypt(enc[i:i+16], plain[i:i+16])
	}
	secret := make([]byte, 32)
	copy(secret, psk[:])
	mac := hmac.New(sha256.New, secret)
	mac.Write(enc)
	hash := sha256.Sum256(psk[:])
	return append(append([]byte{hash[0]}, mac.Sum(nil)[:2]...), enc...)
}

func TestBuildGroupTextMatchesFirmware(t *testing.T) {
	t.Parallel()
	ch := PublicChannel()
	raw, _ := base64.StdEncoding.DecodeString(publicChannelPSK)
	if !bytes.Equal(ch.PSK[:], raw) {
		t.Fatalf("public PSK = %x", ch.PSK)
	}
	now := time.Unix(1_790_000_000, 0)
	got, err := BuildGroupText(ch, "Sunday Net", "Net is open", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte{
		meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeGrpTxt, 0),
		floodPathLen,
	}, firmwareGroupPayload(t, ch.PSK, uint32(now.Unix()), 0, "Sunday Net: Net is open")...)
	if !bytes.Equal(got, want) {
		t.Fatalf("packet\n got %x\nwant %x", got, want)
	}
}

func TestHashtagChannelKey(t *testing.T) {
	t.Parallel()
	sum := sha256.Sum256([]byte("#wnynet"))
	for _, name := range []string{"wnynet", "#wnynet"} {
		if ch := HashtagChannel(name); !bytes.Equal(ch.PSK[:], sum[:16]) {
			t.Errorf("%q PSK = %x, want SHA256(\"#wnynet\")[:16]", name, ch.PSK)
		}
	}
}

func TestGroupTextRoundTrip(t *testing.T) {
	t.Parallel()
	ch := HashtagChannel("#wnynet")
	now := time.Unix(1_790_000_000, 0)
	raw, err := BuildGroupText(ch, "Net Control", "Copied: @[Xero T1]", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := DecodeGroupText(raw, ch)
	if !ok || got.Sender != "Net Control" || got.Text != "Copied: @[Xero T1]" || got.Timestamp != uint32(now.Unix()) {
		t.Fatalf("decoded %+v, %v", got, ok)
	}
	if _, ok := DecodeGroupText(raw, PublicChannel()); ok {
		t.Error("decoded under another channel's key")
	}
	// Same hash byte, wrong key: the MAC must reject it.
	other := ch
	other.PSK[0] ^= 1
	if _, ok := DecodeGroupText(raw, other); ok {
		t.Error("decoded under a wrong key with a matching hash")
	}
	// A non-text type (flags>>2 != 0) is dropped, as the firmware does.
	data := append([]byte{
		meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeGrpTxt, 0), 0,
	}, firmwareGroupPayload(t, ch.PSK, 1, 1<<2, "x: y")...)
	if _, ok := DecodeGroupText(data, ch); ok {
		t.Error("decoded a non-plain-text type")
	}
	if _, ok := DecodeGroupText([]byte{1, 2}, ch); ok {
		t.Error("decoded garbage")
	}
}

func TestGroupTextFitsFirmwareCap(t *testing.T) {
	t.Parallel()
	ch := PublicChannel()
	sender := "Sunday Net"
	short := "Copied: @[A]"
	if FitGroupText(sender, short) != short {
		t.Fatal("trimmed a message that fits")
	}
	// Multi-byte characters straddling the cut must not be split.
	long := strings.Repeat("é", 200)
	raw, err := BuildGroupText(ch, sender, long, nil, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := DecodeGroupText(raw, ch)
	if !ok {
		t.Fatal("didn't decode")
	}
	if n := len(sender + ": " + got.Text); n > MaxGroupTextLen {
		t.Errorf("message is %d bytes, over the firmware's %d", n, MaxGroupTextLen)
	}
	if !utf8.ValidString(got.Text) || !strings.HasSuffix(got.Text, "…") {
		t.Errorf("trimmed text %q", got.Text)
	}
	if exact := strings.Repeat("a", GroupTextRoom(sender)); FitGroupText(sender, exact) != exact {
		t.Error("trimmed a message of exactly the room")
	}
}

func TestScopedGroupText(t *testing.T) {
	t.Parallel()
	key, err := RegionScope("us-ny")
	if err != nil {
		t.Fatal(err)
	}
	// The firmware keys a bare token as the hashtag region "#us-ny".
	sum := sha256.Sum256([]byte("#us-ny"))
	if !bytes.Equal(key[:], sum[:16]) {
		t.Fatalf("region key = %x", key)
	}
	ch := PublicChannel()
	raw, err := BuildGroupText(ch, "NC", "hi", &key, time.Unix(1_790_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	pkt, err := meshcore.PacketFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if pkt.RouteType() != meshcore.RouteTypeTransportFlood || pkt.TransportCode2 != 0 {
		t.Fatalf("route %d, code2 %#x", pkt.RouteType(), pkt.TransportCode2)
	}
	// TransportKey::calcTransportCode: HMAC-SHA256(key, type ‖ payload), first
	// two bytes little-endian.
	mac := hmac.New(sha256.New, key[:])
	mac.Write([]byte{meshcore.PayloadTypeGrpTxt})
	mac.Write(pkt.Payload)
	if want := binary.LittleEndian.Uint16(mac.Sum(nil)); pkt.TransportCode1 != want {
		t.Errorf("transport code = %#x, want %#x", pkt.TransportCode1, want)
	}
	if !meshcore.NewRegionFromHashtag("us-ny").MatchesPacket(pkt) {
		t.Error("a repeater with region us-ny wouldn't match it")
	}
	if got, ok := DecodeGroupText(raw, ch); !ok || got.Text != "hi" {
		t.Errorf("scoped message didn't decode: %+v", got)
	}
	if _, err := RegionScope("$secret"); !errors.Is(err, ErrPrivateRegion) {
		t.Errorf("private region err = %v", err)
	}
}
