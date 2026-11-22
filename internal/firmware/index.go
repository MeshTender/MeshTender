// Package firmware reads the catalog of MeshTender's observer firmware builds
// (index.json on the firmware host, written by the MeshCore fork's
// scripts/meshtender_index.py) and turns a build into the manifest that
// esp-web-tools flashes from.
package firmware

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// indexSchema is the index.json schema this package reads. A different schema
// is refused rather than half-read.
const indexSchema = 1

// StableChannel is the release channel members install from.
const StableChannel = "stable"

// Index is the firmware host's index.json: every build still published, by
// channel and board.
type Index struct {
	Schema      int       `json:"schema"`
	GeneratedAt time.Time `json:"generatedAt"`
	Channels    []Channel `json:"channels"`
}

// Channel is one release channel (stable, beta) and its firmware.
type Channel struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	Firmware []Firmware `json:"firmware"`
}

// Firmware is one PlatformIO environment: a board running one role (room
// server or repeater), with its published builds.
type Firmware struct {
	Env        string  `json:"env"`
	Board      string  `json:"board"`
	Role       string  `json:"role"`
	ChipFamily string  `json:"chipFamily"`
	Latest     string  `json:"latest"`
	Builds     []Build `json:"builds"`
}

// Build is one published build of a Firmware.
type Build struct {
	Version     string    `json:"version"`
	BaseVersion string    `json:"baseVersion"`
	Build       int       `json:"build"`
	Hash        string    `json:"hash"`
	PublishedAt time.Time `json:"publishedAt"`
	// OTA is the app image the device fetches itself (ota update).
	OTA Image `json:"ota"`
	// Full is the merged image (bootloader, partition table, app) flashed over
	// USB, written at Offset.
	Full Image `json:"full"`
}

// Image is one firmware file on the firmware host.
type Image struct {
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Offset int64  `json:"offset"`
}

// Lookup returns the firmware for env on the named channel.
func (idx *Index) Lookup(channel, env string) (Firmware, bool) {
	for _, c := range idx.Channels {
		if c.Name != channel {
			continue
		}
		for _, f := range c.Firmware {
			if f.Env == env {
				return f, true
			}
		}
	}
	return Firmware{}, false
}

// LatestBuild returns the build the index names as latest.
func (f Firmware) LatestBuild() (Build, bool) {
	for _, b := range f.Builds {
		if b.Version == f.Latest {
			return b, true
		}
	}
	return Build{}, false
}

// parseIndex decodes index.json and checks that every image it names is on the
// firmware host itself (same scheme and host as base). The URLs end up in
// manifests the browser fetches and writes to a device, so an index pointing
// anywhere else is refused outright rather than partly trusted.
func parseIndex(data []byte, base *url.URL) (*Index, error) {
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("decode firmware index: %w", err)
	}
	if idx.Schema != indexSchema {
		return nil, fmt.Errorf("firmware index schema %d, want %d", idx.Schema, indexSchema)
	}
	for _, c := range idx.Channels {
		for _, f := range c.Firmware {
			for _, b := range f.Builds {
				for _, img := range []Image{b.OTA, b.Full} {
					if err := checkImage(img, base); err != nil {
						return nil, fmt.Errorf("firmware index: %s %s: %w", f.Env, b.Version, err)
					}
				}
			}
		}
	}
	return &idx, nil
}

func checkImage(img Image, base *url.URL) error {
	if img.Offset < 0 {
		return errors.New("negative image offset")
	}
	u, err := url.Parse(img.URL)
	if err != nil {
		return fmt.Errorf("image URL: %w", err)
	}
	if u.Scheme != base.Scheme || u.Host != base.Host {
		return fmt.Errorf("image URL %q is not on %s://%s", img.URL, base.Scheme, base.Host)
	}
	return nil
}
