package core

import "github.com/MeshTender/MeshTender/internal/config"

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

// radioPresets are the community-suggested region presets shown in the
// dropdown. The list mirrors the "Select Radio Settings" presets in the
// MeshCore companion app.
var radioPresets = []RadioPreset{
	{"au", "Australia — 915.800 MHz · BW250 · SF10 · CR5", 915_800_000, 250_000, 10, 5},
	{"au_narrow", "Australia (Narrow) — 916.575 MHz · BW62.5 · SF7 · CR8", 916_575_000, 62_500, 7, 8},
	{"au_mid", "Australia (Mid) — 915.075 MHz · BW125 · SF9 · CR5", 915_075_000, 125_000, 9, 5},
	{"au_sawa", "Australia: SA, WA — 923.125 MHz · BW62.5 · SF8 · CR8", 923_125_000, 62_500, 8, 8},
	{"au_qld", "Australia: QLD — 923.125 MHz · BW62.5 · SF8 · CR5", 923_125_000, 62_500, 8, 5},
	{"br", "Brazil — 923.125 MHz · BW62.5 · SF8 · CR8", 923_125_000, 62_500, 8, 8},
	{"euuk_narrow", "EU/UK (Narrow) — 869.618 MHz · BW62.5 · SF8 · CR8", 869_618_000, 62_500, 8, 8},
	{"euuk_dep", "EU/UK (Deprecated) — 869.525 MHz · BW250 · SF11 · CR5", 869_525_000, 250_000, 11, 5},
	{"cz_narrow", "Czech Republic (Narrow) — 869.432 MHz · BW62.5 · SF7 · CR5", 869_432_000, 62_500, 7, 5},
	{"eu433_lr", "EU 433 (Long Range) — 433.650 MHz · BW250 · SF11 · CR5", 433_650_000, 250_000, 11, 5},
	{"eu433_narrow", "EU 433 (Narrow) — 433.650 MHz · BW62.5 · SF8 · CR8", 433_650_000, 62_500, 8, 8},
	{"nl", "Netherlands — 869.618 MHz · BW62.5 · SF7 · CR5", 869_618_000, 62_500, 7, 5},
	{"nz", "New Zealand — 917.375 MHz · BW250 · SF11 · CR5", 917_375_000, 250_000, 11, 5},
	{"nz_narrow", "New Zealand (Narrow) — 917.375 MHz · BW62.5 · SF7 · CR5", 917_375_000, 62_500, 7, 5},
	{"pt433", "Portugal 433 — 433.375 MHz · BW62.5 · SF9 · CR6", 433_375_000, 62_500, 9, 6},
	{"pt868", "Portugal 868 — 869.618 MHz · BW62.5 · SF7 · CR6", 869_618_000, 62_500, 7, 6},
	{"ch", "Switzerland — 869.618 MHz · BW62.5 · SF8 · CR8", 869_618_000, 62_500, 8, 8},
	{"usca", "USA/Canada (Recommended) — 910.525 MHz · BW62.5 · SF7 · CR5", 910_525_000, 62_500, 7, 5},
	{"vn_narrow", "Vietnam (Narrow) — 920.250 MHz · BW62.5 · SF8 · CR5", 920_250_000, 62_500, 8, 5},
	{"vn_dep", "Vietnam (Deprecated) — 920.250 MHz · BW250 · SF11 · CR5", 920_250_000, 250_000, 11, 5},
}

// defaultPresetID is the preset selected by default when adding a repeater:
// MeshCore's recommended USA/Canada profile.
const defaultPresetID = "usca"

// defaultPreset returns the preset offered first when adding a repeater.
func defaultPreset() RadioPreset {
	for _, p := range radioPresets {
		if p.ID == defaultPresetID {
			return p
		}
	}
	return radioPresets[0]
}

// presetIDFor returns the preset id whose parameters match d, or "custom" when
// none match. Used to pre-select the dropdown when editing an existing repeater.
func presetIDFor(d config.RadioDefaults) string {
	for _, p := range radioPresets {
		if p.FreqHz == int64(d.FreqHz) && p.BwHz == int64(d.BwHz) && p.SF == int(d.SF) && p.CR == int(d.CR) {
			return p.ID
		}
	}
	return "custom"
}
