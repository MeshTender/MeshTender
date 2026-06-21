// Package config loads MeshTender runtime configuration from the environment.
package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
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
	// scheme/port) — set it to the root registrable domain (e.g.
	// "meshtender.com") so credentials are valid across every subdomain.
	// RPOrigins are the full origins browsers will send a ceremony from;
	// supply a comma-separated list to allow both the auth and app hosts
	// (e.g. "https://auth.meshtender.com,https://app.meshtender.com").
	RPID          string
	RPDisplayName string
	RPOrigins     []string

	// PrimaryHost is the canonical app hostname (no scheme/port) — the host
	// that serves the product. Requests arriving on a different, verified org
	// custom domain serve that org's public page; all other paths there
	// redirect back to PrimaryHost. Defaults to RPID.
	PrimaryHost string

	// AuthHost is the dedicated hostname that serves the login/signup UI and
	// runs WebAuthn ceremonies (e.g. "auth.meshtender.com"). A successful
	// sign-in there hands off to PrimaryHost via a single-use code. When
	// empty, auth is served from PrimaryHost (single-host mode).
	AuthHost string

	// RootHost is the public marketing + organization-discovery hostname (the
	// bare apex, e.g. "meshtender.com" / dev "localhost"). It carries no
	// session (cookies are host-only), so it serves only public content. When
	// empty, that content stays on PrimaryHost.
	RootHost string

	// WWWHost redirects to RootHost (e.g. "www.meshtender.com"). Defaults to
	// "www." + RootHost when RootHost is set.
	WWWHost string

	// Secure reports whether the deployment is HTTPS (derived from RPOrigins).
	// Drives cookie Secure/__Host- prefixing and the scheme of cross-host URLs.
	Secure bool

	// TLSCert and TLSKey, when both set, make the server terminate TLS itself
	// (https). Used for local HTTPS dev (e.g. an mkcert cert for *.example.dev),
	// where HSTS-preloaded TLDs like .dev force the browser onto https. In
	// production TLS usually terminates at a proxy and these stay empty.
	TLSCert string
	TLSKey  string

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
		RPOrigins:     splitOrigins(envOr("MESHTENDER_RP_ORIGIN", "http://localhost:8080")),
		PrimaryHost:   envOr("MESHTENDER_PRIMARY_HOST", envOr("MESHTENDER_RP_ID", "localhost")),
		AuthHost:      os.Getenv("MESHTENDER_AUTH_HOST"),
		RootHost:      os.Getenv("MESHTENDER_ROOT_HOST"),
		WWWHost:       os.Getenv("MESHTENDER_WWW_HOST"),
		TLSCert:       os.Getenv("MESHTENDER_TLS_CERT"),
		TLSKey:        os.Getenv("MESHTENDER_TLS_KEY"),
		DefaultRadio: RadioDefaults{
			FreqHz: uint32(envUintOr("MESHTENDER_RADIO_FREQ_HZ", 869525000)),
			BwHz:   uint32(envUintOr("MESHTENDER_RADIO_BW_HZ", 250000)),
			SF:     uint8(envUintOr("MESHTENDER_RADIO_SF", 11)),
			CR:     uint8(envUintOr("MESHTENDER_RADIO_CR", 5)),
		},
	}

	if c.RootHost != "" && c.WWWHost == "" {
		c.WWWHost = "www." + c.RootHost
	}
	// HTTPS deployments advertise https:// origins; this drives Secure cookies
	// and the scheme used when building absolute cross-host URLs.
	for _, o := range c.RPOrigins {
		if strings.HasPrefix(o, "https://") {
			c.Secure = true
		}
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

// splitOrigins parses a comma-separated list of WebAuthn origins, trimming
// surrounding whitespace and dropping empty entries.
func splitOrigins(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envUintOr(key string, def uint64) uint64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}
