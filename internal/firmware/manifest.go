package firmware

import (
	"errors"
	"fmt"
)

// Manifest is an esp-web-tools install manifest (esp-web-tools src/const.ts,
// interface Manifest). Only the fields MeshTender sets are declared.
type Manifest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// NewInstallPromptErase makes esp-web-tools ask before erasing. Without
	// it, a new install always erases the whole flash, which gives the device
	// a new identity (public key) and drops its settings.
	NewInstallPromptErase bool `json:"new_install_prompt_erase"`
	// NewInstallImprovWaitTime is how many seconds esp-web-tools waits for
	// Improv Wi-Fi after installing; 0 skips it. The observer firmware doesn't
	// speak Improv, so waiting would only delay the end of the install.
	NewInstallImprovWaitTime int             `json:"new_install_improv_wait_time"`
	Builds                   []ManifestBuild `json:"builds"`
}

// ManifestBuild is the image set for one chip family.
type ManifestBuild struct {
	ChipFamily string         `json:"chipFamily"`
	Parts      []ManifestPart `json:"parts"`
}

// ManifestPart is one file written at a flash offset.
type ManifestPart struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
}

// ErrNotFlashable means the firmware can't be installed with esp-web-tools.
var ErrNotFlashable = errors.New("firmware can't be installed from the browser")

// webToolsChips are the chip families esp-web-tools can flash (Build's
// chipFamily union in esp-web-tools src/const.ts).
var webToolsChips = map[string]bool{
	"ESP32": true, "ESP32-C2": true, "ESP32-C3": true, "ESP32-C5": true,
	"ESP32-C6": true, "ESP32-C61": true, "ESP32-H2": true, "ESP32-P4": true,
	"ESP32-S2": true, "ESP32-S3": true, "ESP8266": true,
}

// roleNames are the index's roles as the install dialog names them.
var roleNames = map[string]string{
	"room_server": "room server",
	"repeater":    "repeater",
}

// WebToolsManifest returns the manifest that installs build b of f over USB:
// the merged image, written at its offset. Part paths are absolute URLs on the
// firmware host, which esp-web-tools uses as given.
func WebToolsManifest(f Firmware, b Build) (Manifest, error) {
	if !webToolsChips[f.ChipFamily] {
		return Manifest{}, fmt.Errorf("%w: chip family %q", ErrNotFlashable, f.ChipFamily)
	}
	if b.Full.URL == "" {
		return Manifest{}, fmt.Errorf("%w: %s %s has no merged image", ErrNotFlashable, f.Env, b.Version)
	}
	role := roleNames[f.Role]
	if role == "" {
		role = f.Role
	}
	return Manifest{
		Name:                  fmt.Sprintf("MeshTender observer (%s, %s)", f.Board, role),
		Version:               b.Version,
		NewInstallPromptErase: true,
		Builds: []ManifestBuild{{
			ChipFamily: f.ChipFamily,
			Parts:      []ManifestPart{{Path: b.Full.URL, Offset: b.Full.Offset}},
		}},
	}, nil
}
