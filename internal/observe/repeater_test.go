package observe

import (
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/observe/observetest"
)

func TestDecodeAdvertLocation(t *testing.T) {
	t.Parallel()
	id := newIdentity(t)
	cases := []struct {
		name     string
		lat, lon float64
		want     bool
	}{
		{"located", 42.886447, -78.878369, true},
		{"none", 0, 0, false},
	}
	for _, c := range cases {
		raw := observetest.Advert{Type: "REPEATER", Name: "Hilltop", Lat: c.lat, Lon: c.lon,
			Route: meshcore.RouteTypeFlood}.Packet(t, id, 1_790_000_000)
		a, err := DecodeAdvert(raw)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !a.Verify() {
			t.Errorf("%s: signature doesn't verify", c.name)
		}
		if got := a.Lat != nil; got != c.want {
			t.Fatalf("%s: has location = %v, want %v", c.name, got, c.want)
		}
		if c.want && (*a.Lat-c.lat > 1e-6 || *a.Lat-c.lat < -1e-6 || *a.Lon-c.lon > 1e-6 || *a.Lon-c.lon < -1e-6) {
			t.Errorf("%s: location = %v,%v, want %v,%v", c.name, *a.Lat, *a.Lon, c.lat, c.lon)
		}
		if a.NodeType != meshcore.AdvertTypeRepeater {
			t.Errorf("%s: node type = %d", c.name, a.NodeType)
		}
	}
}

// A repeater's own flood advert leaves with zero hops but its path_len still
// carries the configured hash size; a zero-hop advert's never does.
func TestDecodeAdvertHashSizeAtOrigin(t *testing.T) {
	t.Parallel()
	id := newIdentity(t)
	for size := 1; size <= 3; size++ {
		a, err := DecodeAdvert(observetest.Advert{Type: "REPEATER", Route: meshcore.RouteTypeFlood,
			HashSize: size}.Packet(t, id, 1))
		if err != nil {
			t.Fatal(err)
		}
		if !a.Flood() || a.ZeroHop() || int(a.PathHashSize) != size || len(a.Path) != 0 {
			t.Errorf("size %d: flood=%v zeroHop=%v size=%d path=%d", size, a.Flood(), a.ZeroHop(), a.PathHashSize, len(a.Path))
		}
	}
	a, err := DecodeAdvert(observetest.Advert{Type: "REPEATER", Route: meshcore.RouteTypeDirect}.Packet(t, id, 1))
	if err != nil {
		t.Fatal(err)
	}
	if a.Flood() || !a.ZeroHop() {
		t.Errorf("zero-hop: flood=%v zeroHop=%v", a.Flood(), a.ZeroHop())
	}
	scoped, err := DecodeAdvert(observetest.Advert{Type: "REPEATER", Scope: "buf", HashSize: 2}.Packet(t, id, 1))
	if err != nil {
		t.Fatal(err)
	}
	if !scoped.Flood() || scoped.PathHashSize != 2 || scoped.TransportCode1 == 0 {
		t.Errorf("scoped: flood=%v size=%d code=%#x", scoped.Flood(), scoped.PathHashSize, scoped.TransportCode1)
	}
}

func TestMatchScope(t *testing.T) {
	t.Parallel()
	id := newIdentity(t)
	cands := []string{"wny", "buf", "roc"}
	cases := []struct {
		name string
		adv  observetest.Advert
		want Scope
	}{
		{"unscoped", observetest.Advert{Type: "REPEATER", Route: meshcore.RouteTypeFlood}, Scope{}},
		{"known", observetest.Advert{Type: "REPEATER", Scope: "buf"}, Scope{Scoped: true, Region: "buf"}},
		// An explicit '#' names the same region as the bare token.
		{"hashtag", observetest.Advert{Type: "REPEATER", Scope: "#roc"}, Scope{Scoped: true, Region: "roc"}},
		{"unknown", observetest.Advert{Type: "REPEATER", Scope: "syr"}, Scope{Scoped: true}},
		// Region names are case-sensitive in the key derivation.
		{"case", observetest.Advert{Type: "REPEATER", Scope: "BUF"}, Scope{Scoped: true}},
	}
	for _, c := range cases {
		if got := MatchScope(c.adv.Packet(t, id, 1_790_000_000), cands); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	if got := MatchScope([]byte{0xff}, cands); got != (Scope{}) {
		t.Errorf("garbage: %+v", got)
	}
}

func heardEvery(start time.Time, gaps ...time.Duration) []time.Time {
	out := []time.Time{start}
	for _, g := range gaps {
		start = start.Add(g)
		out = append(out, start)
	}
	return out
}

func TestEstimateFloodInterval(t *testing.T) {
	t.Parallel()
	h := time.Hour
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		heard []time.Time
		want  FloodInterval
		ok    bool
	}{
		{"steady 47h", heardEvery(t0, 47*h, 47*h, 47*h), FloodInterval{47, 3}, true},
		{"old 3h default", heardEvery(t0, 3*h, 3*h+40*time.Second, 3*h-20*time.Second, 3*h), FloodInterval{3, 4}, true},
		{"missed advert", heardEvery(t0, 12*h, 24*h, 12*h, 36*h), FloodInterval{12, 4}, true},
		// A manual `advert` 20 h in splits one gap; the timer carries on.
		{"manual advert", heardEvery(t0, 47*h, 20*h, 27*h, 47*h), FloodInterval{47, 2}, true},
		// A reboot 10 h after an advert restarts the timer: one 57 h gap.
		{"reboot", heardEvery(t0, 47*h, 57*h, 47*h), FloodInterval{47, 2}, true},
		{"one gap", heardEvery(t0, 47*h), FloodInterval{}, false},
		{"no agreement", heardEvery(t0, 47*h, 30*h, 12*h), FloodInterval{}, false},
		{"nothing", nil, FloodInterval{}, false},
	}
	for _, c := range cases {
		got, ok := EstimateFloodInterval(c.heard)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
	// Order of the input doesn't matter.
	rev := heardEvery(t0, 12*h, 12*h, 12*h)
	rev[0], rev[3] = rev[3], rev[0]
	if got, ok := EstimateFloodInterval(rev); !ok || got.Hours != 12 {
		t.Errorf("unsorted: %+v %v", got, ok)
	}
}

func TestFloodIntervalVintage(t *testing.T) {
	t.Parallel()
	cases := map[int]Vintage{3: VintagePre18, 12: Vintage18, 47: VintageCurrent, 48: VintageCustom, 24: VintageCustom}
	for h, want := range cases {
		if got := FloodIntervalVintage(h); got != want {
			t.Errorf("%d h: %v, want %v", h, got, want)
		}
	}
	if !VintagePre18.Outdated() || !Vintage18.Outdated() || VintageCurrent.Outdated() || VintageCustom.Outdated() {
		t.Error("Outdated misclassifies")
	}
}
