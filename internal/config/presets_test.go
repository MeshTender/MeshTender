package config

import "testing"

// TestPresetName: settings matching a preset are named as the companion app
// lists them (the label before " — "); anything else is custom and unnamed.
func TestPresetName(t *testing.T) {
	t.Parallel()
	usca := RadioDefaults{FreqHz: 910_525_000, BwHz: 62_500, SF: 7, CR: 5}
	if got := usca.PresetName(); got != "USA/Canada (Recommended)" {
		t.Errorf("USA/Canada settings: PresetName() = %q", got)
	}
	if id := PresetIDFor(usca); id != "usca" {
		t.Errorf("PresetIDFor(USA/Canada) = %q, want usca", id)
	}
	custom := RadioDefaults{FreqHz: 915_000_000, BwHz: 125_000, SF: 9, CR: 6}
	if got := custom.PresetName(); got != "" {
		t.Errorf("custom settings: PresetName() = %q, want empty", got)
	}
	if id := PresetIDFor(custom); id != "custom" {
		t.Errorf("PresetIDFor(custom) = %q, want custom", id)
	}
}
