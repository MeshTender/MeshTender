// Package config loads MeshTender runtime configuration from the environment.
package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
)

// Config holds all runtime configuration for the server.
type Config struct {
	// Addr is the TCP address the HTTP server listens on (e.g. ":8080").
	Addr string
	// DatabaseURL is the Postgres connection string (pgx-compatible DSN).
	DatabaseURL string

	// MasterKey is a 32-byte AES key used to encrypt the server identity
	// seed at rest. Supplied as 64 hex chars via MESHTENDER_MASTER_KEY.
	MasterKey [32]byte

	// WebAuthn relying-party settings. RPID is the effective domain (no
	// scheme/port), RPOrigins are the full origins browsers will send.
	RPID          string
	RPDisplayName string
	RPOrigins     []string

	// DefaultRadio holds fallback LoRa parameters offered when adding a
	// repeater. Per-repeater values in the DB take precedence.
	DefaultRadio RadioDefaults
}

// RadioDefaults are the suggested LoRa parameters for a new repeater.
type RadioDefaults struct {
	FreqHz uint32
	BwHz   uint32
	SF     uint8
	CR     uint8
}

// Load reads configuration from the environment, applying defaults and
// validating required fields.
func Load() (*Config, error) {
	c := &Config{
		Addr:          envOr("MESHTENDER_ADDR", ":8080"),
		DatabaseURL:   os.Getenv("MESHTENDER_DATABASE_URL"),
		RPID:          envOr("MESHTENDER_RP_ID", "localhost"),
		RPDisplayName: envOr("MESHTENDER_RP_NAME", "MeshTender"),
		RPOrigins:     []string{envOr("MESHTENDER_RP_ORIGIN", "http://localhost:8080")},
		DefaultRadio: RadioDefaults{
			FreqHz: uint32(envUintOr("MESHTENDER_RADIO_FREQ_HZ", 869525000)),
			BwHz:   uint32(envUintOr("MESHTENDER_RADIO_BW_HZ", 250000)),
			SF:     uint8(envUintOr("MESHTENDER_RADIO_SF", 11)),
			CR:     uint8(envUintOr("MESHTENDER_RADIO_CR", 5)),
		},
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("MESHTENDER_DATABASE_URL is required")
	}

	rawKey := os.Getenv("MESHTENDER_MASTER_KEY")
	if rawKey == "" {
		return nil, fmt.Errorf("MESHTENDER_MASTER_KEY is required (64 hex chars)")
	}
	keyBytes, err := hex.DecodeString(rawKey)
	if err != nil {
		return nil, fmt.Errorf("MESHTENDER_MASTER_KEY must be hex: %w", err)
	}
	if len(keyBytes) != 32 {
		return nil, fmt.Errorf("MESHTENDER_MASTER_KEY must decode to 32 bytes, got %d", len(keyBytes))
	}
	copy(c.MasterKey[:], keyBytes)

	return c, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envUintOr(key string, def uint64) uint64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}
