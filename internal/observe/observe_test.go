package observe

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// newAdvertPacket builds the raw bytes of an advert as firmware would put it on
// air: signed by id, routed as route with the given path (hashSize bytes per
// entry). Fixtures are synthesized so no real member's key or location lands in
// the repo.
func newAdvertPacket(t *testing.T, id meshcore.LocalIdentity, ts uint32, name string, route byte, hashSize int, path [][]byte) []byte {
	t.Helper()
	app, err := (&meshcore.AdvertAppData{Type: "CHAT", Name: name}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	adv := &meshcore.Advert{PublicKey: id.Identity, Timestamp: ts, RawAppData: app}
	adv.SignWith(id)
	payload, err := adv.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	var flat []byte
	for _, h := range path {
		flat = append(flat, h...)
	}
	pkt := &meshcore.Packet{
		Header:     meshcore.MakeHeader(route, meshcore.PayloadTypeAdvert, 0),
		PathLength: byte(hashSize-1)<<6 | byte(len(path)),
		Path:       flat,
		Payload:    payload,
	}
	raw, err := pkt.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newIdentity(t *testing.T) meshcore.LocalIdentity {
	t.Helper()
	id, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDecodeAdvertRoutes(t *testing.T) {
	t.Parallel()
	id := newIdentity(t)
	ts := uint32(1_790_000_000)
	path3 := [][]byte{{0x35, 0x2c, 0x30}, {0x8b, 0xfc, 0xfc}}

	cases := []struct {
		name     string
		route    byte
		hashSize int
		path     [][]byte
		zeroHop  bool
	}{
		{"flood 3-byte path", meshcore.RouteTypeFlood, 3, path3, false},
		{"flood 1-byte path", meshcore.RouteTypeFlood, 1, [][]byte{{0x35}, {0x8b}, {0x01}}, false},
		{"flood 2-byte path", meshcore.RouteTypeFlood, 2, [][]byte{{0x35, 0x2c}}, false},
		{"flood heard direct by observer", meshcore.RouteTypeFlood, 1, nil, false},
		{"zero-hop", meshcore.RouteTypeDirect, 1, nil, true},
		{"transport flood", meshcore.RouteTypeTransportFlood, 3, path3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := newAdvertPacket(t, id, ts, "Net Check", tc.route, tc.hashSize, tc.path)
			a, err := DecodeAdvert(raw)
			if err != nil {
				t.Fatalf("DecodeAdvert: %v", err)
			}
			if a.PublicKey.String() != id.String() {
				t.Errorf("pubkey = %s, want %s", a.PublicKey, id)
			}
			if a.Timestamp != ts || a.Name != "Net Check" || a.NodeType != meshcore.AdvertTypeChat {
				t.Errorf("got ts=%d name=%q type=%d", a.Timestamp, a.Name, a.NodeType)
			}
			if a.RouteType != tc.route || int(a.PathHashSize) != tc.hashSize {
				t.Errorf("route=%d size=%d, want %d/%d", a.RouteType, a.PathHashSize, tc.route, tc.hashSize)
			}
			if len(a.Path) != len(tc.path) {
				t.Fatalf("path len = %d, want %d", len(a.Path), len(tc.path))
			}
			for i := range tc.path {
				if hex.EncodeToString(a.Path[i]) != hex.EncodeToString(tc.path[i]) {
					t.Errorf("path[%d] = %x, want %x", i, a.Path[i], tc.path[i])
				}
			}
			if a.ZeroHop() != tc.zeroHop {
				t.Errorf("ZeroHop = %v, want %v", a.ZeroHop(), tc.zeroHop)
			}
			if !a.Verify() {
				t.Error("valid advert failed to verify")
			}
		})
	}
}

func TestDecodeAdvertRejectsTamperedSignature(t *testing.T) {
	t.Parallel()
	raw := newAdvertPacket(t, newIdentity(t), 1_790_000_000, "Mallory", meshcore.RouteTypeFlood, 1, nil)
	raw[len(raw)-1] ^= 0x01 // flip a bit of the signed name
	a, err := DecodeAdvert(raw)
	if err != nil {
		t.Fatalf("DecodeAdvert: %v", err)
	}
	if a.Verify() {
		t.Fatal("tampered advert verified")
	}
}

func TestDecodeAdvertRejectsOtherKeyClaim(t *testing.T) {
	t.Parallel()
	// Signed by one key, claiming another: what forging someone's check-in looks like.
	victim, forger := newIdentity(t), newIdentity(t)
	app, _ := (&meshcore.AdvertAppData{Type: "CHAT", Name: "Victim"}).ToBytes()
	adv := &meshcore.Advert{PublicKey: victim.Identity, Timestamp: 1_790_000_000, RawAppData: app}
	adv.SignWith(forger)
	payload, _ := adv.ToBytes()
	raw, _ := (&meshcore.Packet{
		Header:  meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeAdvert, 0),
		Payload: payload,
	}).ToBytes()
	a, err := DecodeAdvert(raw)
	if err != nil {
		t.Fatal(err)
	}
	if a.Verify() {
		t.Fatal("advert signed by another key verified")
	}
}

func TestDecodeAdvertNonAdvertAndGarbage(t *testing.T) {
	t.Parallel()
	// A group text packet: header 0x15 (flood, GRP_TXT), no path, some payload.
	grp, _ := hex.DecodeString("1500" + strings.Repeat("ab", 20))
	if _, err := DecodeAdvert(grp); !errors.Is(err, ErrNotAdvert) {
		t.Errorf("group text: err = %v, want ErrNotAdvert", err)
	}

	good := newAdvertPacket(t, newIdentity(t), 1_790_000_000, "X", meshcore.RouteTypeFlood, 1, nil)
	for name, raw := range map[string][]byte{
		"empty":           nil,
		"header only":     {0x11},
		"truncated":       good[:50],
		"path overruns":   {0x11, 0x05, 0x01},
		"invalid hash sz": {0x11, 0xC1, 0x01},
	} {
		if _, err := DecodeAdvert(raw); err == nil || errors.Is(err, ErrNotAdvert) {
			t.Errorf("%s: err = %v, want a decode error", name, err)
		}
	}
}

func TestAdvertSkew(t *testing.T) {
	t.Parallel()
	a := &Advert{Timestamp: 1_790_000_000}
	heard := time.Unix(1_790_000_090, 0)
	if got := a.Skew(heard); got != 90*time.Second {
		t.Errorf("Skew = %v, want 90s", got)
	}
}

func TestParseTopic(t *testing.T) {
	t.Parallel()
	id := newIdentity(t)
	upper := strings.ToUpper(id.String())

	got, err := ParseTopic("meshcore", "meshcore/BUF/"+upper+"/packets")
	if err != nil {
		t.Fatal(err)
	}
	if got.IATA != "BUF" || got.Kind != KindPackets || got.Observer.String() != id.String() {
		t.Errorf("got %+v", got)
	}
	if got, err := ParseTopic("meshcore", "meshcore/YYZ/"+upper+"/status"); err != nil || got.Kind != KindStatus {
		t.Errorf("status: %+v %v", got, err)
	}

	for _, topic := range []string{
		"other/BUF/" + upper + "/packets",
		"meshcore/BUF/" + upper + "/internal",
		"meshcore/BUF/" + upper,
		"meshcore/BUF/" + upper + "/packets/extra",
	} {
		if _, err := ParseTopic("meshcore", topic); !errors.Is(err, ErrForeignTopic) {
			t.Errorf("%s: err = %v, want ErrForeignTopic", topic, err)
		}
	}
	for _, topic := range []string{
		"meshcore/BUF/nothex/packets",
		"meshcore/BUF/abcd/packets",
		"meshcore//" + upper + "/packets",
	} {
		if _, err := ParseTopic("meshcore", topic); err == nil || errors.Is(err, ErrForeignTopic) {
			t.Errorf("%s: err = %v, want a malformed-topic error", topic, err)
		}
	}
}

func TestParsePacket(t *testing.T) {
	t.Parallel()
	// Shapes seen from different uplinks: numeric and string signal fields,
	// extra fields, and none at all.
	p, err := ParsePacket([]byte(`{"type":"PACKET","raw":"11AB","SNR":5.75,"RSSI":-85,"len":"2"}`))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(p.Raw) != "11ab" || p.SNR == nil || *p.SNR != 5.75 || p.RSSI == nil || *p.RSSI != -85 {
		t.Errorf("numeric: %+v", p)
	}
	p, err = ParsePacket([]byte(`{"raw":"11ab","SNR":"-3.25","RSSI":"-101"}`))
	if err != nil || *p.SNR != -3.25 || *p.RSSI != -101 {
		t.Errorf("string: %+v %v", p, err)
	}
	p, err = ParsePacket([]byte(`{"raw":"11ab","SNR":"n/a"}`))
	if err != nil || p.SNR != nil || p.RSSI != nil {
		t.Errorf("absent/junk: %+v %v", p, err)
	}

	for name, body := range map[string]string{
		"not json": `garbage`,
		"no raw":   `{"SNR":1}`,
		"bad hex":  `{"raw":"zz"}`,
	} {
		if _, err := ParsePacket([]byte(body)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestParseStatus(t *testing.T) {
	t.Parallel()
	s, err := ParseStatus([]byte(`{"status":"online","client_version":"meshcore-ha:2.9.0",
		"firmware_version":"v1.17.1","model":"Heltec V3",
		"stats":{"battery_mv":3999,"noise_floor":-110,"uptime_secs":1219208}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Online || s.ClientVersion != "meshcore-ha:2.9.0" || s.FirmwareVersion != "v1.17.1" || s.Model != "Heltec V3" {
		t.Errorf("strings: %+v", s)
	}
	if *s.BatteryMV != 3999 || *s.NoiseFloor != -110 || *s.UptimeSecs != 1219208 {
		t.Errorf("stats: %+v", s)
	}

	s, err = ParseStatus([]byte(`{"status":"offline"}`))
	if err != nil || s.Online || s.BatteryMV != nil {
		t.Errorf("sparse: %+v %v", s, err)
	}
}

func TestPathResolver(t *testing.T) {
	t.Parallel()
	r := NewPathResolver([]RepeaterKey{
		{PublicID: "a", Name: "Alpha", PublicKeyHex: "352C30" + strings.Repeat("00", 29)},
		{PublicID: "b", Name: "Bravo", PublicKeyHex: "35aa11" + strings.Repeat("00", 29)},
		{PublicID: "c", Name: "Charlie", PublicKeyHex: "8bfcfc" + strings.Repeat("00", 29)},
	})

	hops := r.Resolve([][]byte{{0x35, 0x2c, 0x30}, {0x8b, 0xfc, 0xfc}, {0x99, 0x99, 0x99}})
	if len(hops) != 3 {
		t.Fatalf("hops = %d", len(hops))
	}
	if hops[0].Hash != "352c30" || len(hops[0].Candidates) != 1 || hops[0].Candidates[0].Name != "Alpha" {
		t.Errorf("hop 0: %+v", hops[0])
	}
	if len(hops[1].Candidates) != 1 || hops[1].Candidates[0].Name != "Charlie" {
		t.Errorf("hop 1: %+v", hops[1])
	}
	if len(hops[2].Candidates) != 0 {
		t.Errorf("unknown hop resolved: %+v", hops[2])
	}

	// A 1-byte hash collides: both repeaters starting 0x35 are candidates.
	one := r.Resolve([][]byte{{0x35}})
	if len(one[0].Candidates) != 2 {
		t.Errorf("1-byte 0x35 candidates = %+v, want Alpha and Bravo", one[0].Candidates)
	}
}
