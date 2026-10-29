package web

import (
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/observe"
	"github.com/MeshTender/MeshTender/internal/observe/observetest"
	"github.com/MeshTender/MeshTender/internal/store"
)

func TestNewHeardView(t *testing.T) {
	t.Parallel()
	if NewHeardView(nil, "x", nil, nil, nil) != nil {
		t.Fatal("nil observation gave a view")
	}
	f := func(v float64) *float64 { return &v }
	regions := []store.RegionName{{Token: "wny", DisplayName: "Western NY"}, {Token: "buf", DisplayName: "Buffalo"}}
	packet := observetest.Advert{Type: "REPEATER", Scope: "buf"}.Packet(t, observetest.NewIdentity(t), 1)

	o := &store.RepeaterObservation{AdvertName: "Hill", AdvertLat: f(42.9), AdvertLon: f(-78.8),
		ClockOffset: 90 * time.Second, LastFloodPacket: packet,
		FloodInterval: &observe.FloodInterval{Hours: 12, Samples: 4}}
	// 0.0005° of latitude is ~56 m: the same site.
	h := NewHeardView(o, "Hill", f(42.9005), f(-78.8), regions)
	if h.NameDiffers || h.LocationDiffers || h.ClockOff {
		t.Errorf("flagged a match: name %v location %v (%.0f m) clock %v", h.NameDiffers, h.LocationDiffers, h.LocationOffM, h.ClockOff)
	}
	if h.ScopeName != "Buffalo" || h.Scope.Region != "buf" {
		t.Errorf("scope = %+v %q", h.Scope, h.ScopeName)
	}
	if !h.Vintage.Outdated() || h.VintageLabel() != "Old default (1.8–1.15)" {
		t.Errorf("vintage = %v %q", h.Vintage, h.VintageLabel())
	}

	o.ClockOffset = -3 * 24 * time.Hour
	h = NewHeardView(o, "Hill (old)", f(43.0), f(-78.8), nil)
	if !h.NameDiffers || !h.LocationDiffers || h.LocationOffText() != "11.1 km" {
		t.Errorf("missed a difference: name %v location %v %q", h.NameDiffers, h.LocationDiffers, h.LocationOffText())
	}
	if !h.ClockOff || h.ClockBehind() || h.ClockOffText() != "3 days" {
		t.Errorf("clock: off %v behind %v %q", h.ClockOff, h.ClockBehind(), h.ClockOffText())
	}
	if h.Scope != (observe.Scope{Scoped: true}) || h.ScopeName != "" {
		t.Errorf("scope with no regions = %+v %q", h.Scope, h.ScopeName)
	}
	// Nothing registered: any advertised location differs.
	if h = NewHeardView(o, "Hill", nil, nil, nil); !h.LocationDiffers {
		t.Error("advertised location with none registered not flagged")
	}
}

func TestHumanDuration(t *testing.T) {
	t.Parallel()
	cases := map[time.Duration]string{
		45 * time.Minute: "45 minutes", time.Minute: "1 minute", time.Hour: "1 hour",
		24 * time.Hour: "24 hours", 47 * time.Hour: "47 hours", 72 * time.Hour: "3 days",
	}
	for d, want := range cases {
		if got := HumanDuration(d); got != want {
			t.Errorf("HumanDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
