package core

import "github.com/MeshTender/MeshTender/internal/config"

// defaultPresetID is the preset selected by default when adding a repeater:
// MeshCore's recommended USA/Canada profile.
const defaultPresetID = "usca"

// defaultPreset returns the preset offered first when adding a repeater.
func defaultPreset() config.RadioPreset {
	for _, p := range config.RadioPresets {
		if p.ID == defaultPresetID {
			return p
		}
	}
	return config.RadioPresets[0]
}

// radioFields is the data for the shared "radio-fields" template: the preset
// picker, preselected to whichever preset matches radio, plus its values.
type radioFields struct {
	Presets  []config.RadioPreset
	Selected string
	Radio    config.RadioDefaults
	Named    bool // submit the values with the form
}

func newRadioFields(radio config.RadioDefaults, named bool) radioFields {
	return radioFields{Presets: config.RadioPresets, Selected: config.PresetIDFor(radio), Radio: radio, Named: named}
}

// defaultRadio is the default preset as radio settings.
func defaultRadio() config.RadioDefaults {
	p := defaultPreset()
	return config.RadioDefaults{FreqHz: uint32(p.FreqHz), BwHz: uint32(p.BwHz), SF: uint8(p.SF), CR: uint8(p.CR)} //nolint:gosec // G115: preset constants
}
