package web

import "github.com/jleight/meshtender/internal/config"

// RadioPreset is a convenience set of LoRa parameters for a MeshCore region,
// offered in the add-repeater form. They are starting points — regions vary and
// migrate over time, so the form also allows a fully custom entry.
type RadioPreset struct {
	ID     string
	Label  string
	FreqHz int64
	BwHz   int64
	SF     int
	CR     int
}

// radioPresets are the region presets shown in the dropdown.
var radioPresets = []RadioPreset{
	{"eu868", "Europe 868 — 869.525 MHz · BW250 · SF11 · CR5", 869_525_000, 250_000, 11, 5},
	{"eu433", "Europe 433 — 433.525 MHz · BW250 · SF11 · CR5", 433_525_000, 250_000, 11, 5},
	{"us915", "US / Canada 915 — 910.525 MHz · BW250 · SF11 · CR5", 910_525_000, 250_000, 11, 5},
	{"us915n", "US / Canada 915 narrow — 910.525 MHz · BW62.5 · SF7 · CR5", 910_525_000, 62_500, 7, 5},
	{"anz915", "Australia / NZ — 915.800 MHz · BW250 · SF10 · CR5", 915_800_000, 250_000, 10, 5},
}

// defaultPresetID returns the preset id matching the configured defaults, or
// "custom" when no preset matches.
func defaultPresetID(d config.RadioDefaults) string {
	for _, p := range radioPresets {
		if p.FreqHz == int64(d.FreqHz) && p.BwHz == int64(d.BwHz) && p.SF == int(d.SF) && p.CR == int(d.CR) {
			return p.ID
		}
	}
	return "custom"
}
