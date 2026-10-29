package observe

import (
	"slices"
	"time"
)

// Flood advert interval limits and defaults, from the repeater firmware
// (examples/simple_repeater, CommonCLI.cpp `set flood.advert.interval`): whole
// hours, 0 (off) or 3–168. The default was 3 h until v1.8.0, 12 h through
// v1.15.x, and 47 h from v1.16.0.
const (
	MinFloodAdvertHours = 3
	MaxFloodAdvertHours = 168

	FloodAdvertDefaultPre18   = 3
	FloodAdvertDefault18      = 12
	FloodAdvertDefaultCurrent = 47
)

// FloodInterval is an estimate of a repeater's flood advert interval.
type FloodInterval struct {
	Hours int
	// Samples is how many of the gaps it was drawn from fit it: exactly one
	// interval, or a whole number of them (adverts the observers missed).
	Samples int
}

// EstimateFloodInterval estimates the flood advert interval from when a
// repeater's flood adverts were first heard (our clock — the sender's can
// reset on reboot). The firmware's timer has no jitter, so each gap rounds to
// a whole hour; the answer is the most common gap in the configurable range,
// seen at least twice, preferring the shorter on a tie (a longer one is often
// a multiple from a missed advert). What disturbs the timer doesn't repeat: a
// manual `advert` adds an extra flood advert without resetting it, splitting
// one gap in two, and a reboot restarts it, cutting one short.
func EstimateFloodInterval(heard []time.Time) (FloodInterval, bool) {
	sorted := slices.Clone(heard)
	slices.SortFunc(sorted, time.Time.Compare)
	var gaps []int
	counts := map[int]int{}
	for i := 1; i < len(sorted); i++ {
		h := int(sorted[i].Sub(sorted[i-1]).Round(time.Hour) / time.Hour)
		if h < MinFloodAdvertHours {
			continue
		}
		gaps = append(gaps, h)
		if h <= MaxFloodAdvertHours {
			counts[h]++
		}
	}
	best := 0
	for h, n := range counts {
		if n > counts[best] || (n == counts[best] && h < best) {
			best = h
		}
	}
	if counts[best] < 2 {
		return FloodInterval{}, false
	}
	fi := FloodInterval{Hours: best}
	for _, g := range gaps {
		if g%best == 0 {
			fi.Samples++
		}
	}
	return fi, true
}

// Vintage says which firmware default an interval matches, if any.
type Vintage int

const (
	VintageCustom  Vintage = iota // not a firmware default
	VintagePre18                  // 3 h, the default before v1.8.0
	Vintage18                     // 12 h, the default for v1.8.0–v1.15.x
	VintageCurrent                // 47 h, the default from v1.16.0
)

// FloodIntervalVintage classifies hours against the firmware defaults.
func FloodIntervalVintage(hours int) Vintage {
	switch hours {
	case FloodAdvertDefaultPre18:
		return VintagePre18
	case FloodAdvertDefault18:
		return Vintage18
	case FloodAdvertDefaultCurrent:
		return VintageCurrent
	}
	return VintageCustom
}

// Outdated reports whether v is an old firmware default — a repeater that was
// likely never reconfigured after the default changed.
func (v Vintage) Outdated() bool {
	return v == VintagePre18 || v == Vintage18
}
