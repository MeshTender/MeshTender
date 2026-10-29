package web

import (
	"fmt"
	"strconv"
	"time"

	"github.com/MeshTender/MeshTender/internal/geo"
	"github.com/MeshTender/MeshTender/internal/observe"
	"github.com/MeshTender/MeshTender/internal/store"
)

// Thresholds past which what a repeater advertises is flagged as differing
// from what's registered.
const (
	// heardLocationTolerance absorbs coordinates entered by hand vs. set on the
	// device: the same site, a few meters apart.
	heardLocationTolerance = 100.0 // meters
	// heardClockTolerance is how far off a repeater's clock may be before it's
	// shown: adverts are timestamped by it, so it's worth fixing when it's wrong.
	heardClockTolerance = 2 * time.Minute
)

// HeardView is what a repeater's own adverts say about it, laid beside what's
// registered for it: the "Heard on the mesh" card and the org list's summary.
type HeardView struct {
	*store.RepeaterObservation

	// NameDiffers: the advertised name isn't the registered one.
	NameDiffers bool
	// LocationDiffers: the repeater advertises a location, and either none is
	// registered or it's more than heardLocationTolerance from it (LocationOffM;
	// zero when none is registered).
	LocationDiffers bool
	LocationOffM    float64
	locationKnown   bool // a location is registered

	// Scope is the flood scope of its newest flood advert; ScopeName is the
	// matched region's label.
	Scope     observe.Scope
	ScopeName string

	// Vintage classifies FloodInterval, when there is one.
	Vintage observe.Vintage
	// ClockOff: the repeater's clock is off by more than heardClockTolerance.
	ClockOff bool
}

// NewHeardView compares an observation against a repeater's registered name
// and location, and matches its scope against regions. A nil o is nil.
func NewHeardView(o *store.RepeaterObservation, name string, lat, lon *float64, regions []store.RegionName) *HeardView {
	if o == nil {
		return nil
	}
	h := &HeardView{RepeaterObservation: o, NameDiffers: o.AdvertName != "" && o.AdvertName != name}
	if o.AdvertLat != nil && o.AdvertLon != nil {
		if lat != nil && lon != nil {
			h.locationKnown = true
			h.LocationOffM = geo.Distance(*lat, *lon, *o.AdvertLat, *o.AdvertLon)
			h.LocationDiffers = h.LocationOffM > heardLocationTolerance
		} else {
			h.LocationDiffers = true
		}
	}
	if o.LastFloodPacket != nil {
		tokens := make([]string, len(regions))
		for i, r := range regions {
			tokens[i] = r.Token
		}
		h.Scope = observe.MatchScope(o.LastFloodPacket, tokens)
		for _, r := range regions {
			if r.Token == h.Scope.Region {
				h.ScopeName = r.DisplayName
				break
			}
		}
	}
	if o.FloodInterval != nil {
		h.Vintage = observe.FloodIntervalVintage(o.FloodInterval.Hours)
	}
	h.ClockOff = o.ClockOffset > heardClockTolerance || o.ClockOffset < -heardClockTolerance
	return h
}

// ClockBehind reports whether the repeater's clock is behind (or else ahead).
func (h *HeardView) ClockBehind() bool { return h.ClockOffset > 0 }

// ClockOffText is how far off the clock is, in its largest whole unit.
func (h *HeardView) ClockOffText() string {
	d := h.ClockOffset
	if d < 0 {
		d = -d
	}
	return HumanDuration(d)
}

// LocationOffText is LocationOffM for reading: "350 m", "2.4 km".
func (h *HeardView) LocationOffText() string {
	if h.LocationOffM < 1000 {
		return fmt.Sprintf("%.0f m", h.LocationOffM)
	}
	return fmt.Sprintf("%.1f km", h.LocationOffM/1000)
}

// AdvertLocationText is the advertised location for reading, "" for none.
func (h *HeardView) AdvertLocationText() string {
	if h.AdvertLat == nil || h.AdvertLon == nil {
		return ""
	}
	return strconv.FormatFloat(*h.AdvertLat, 'f', -1, 64) + ", " + strconv.FormatFloat(*h.AdvertLon, 'f', -1, 64)
}

// LocationNote says what location the repeater advertises, and how far it
// is from the registered one when there is one.
func (h *HeardView) LocationNote() string {
	if h.locationKnown {
		return "It advertises " + h.AdvertLocationText() + ", " + h.LocationOffText() + " from this."
	}
	return "It advertises " + h.AdvertLocationText() + "."
}

// VintageLabel names the firmware default the flood interval matches, ""
// for a custom one.
func (h *HeardView) VintageLabel() string {
	switch h.Vintage {
	case observe.VintagePre18:
		return "Old default (before 1.8)"
	case observe.Vintage18:
		return "Old default (1.8–1.15)"
	case observe.VintageCurrent:
		return "Current default"
	}
	return ""
}

// RecommendedFloodHours is the current firmware default, for the hint that
// an outdated repeater should move to it.
func (h *HeardView) RecommendedFloodHours() int { return observe.FloodAdvertDefaultCurrent }
