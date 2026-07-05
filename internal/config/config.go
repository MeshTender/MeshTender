// Package config loads MeshTender runtime configuration from the environment.
package config

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
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
	// sign-in there hands off to PrimaryHost via a single-use code. Required.
	AuthHost string

	// RootHost is the public marketing + organization-discovery hostname (the
	// bare apex, e.g. "meshtender.com" / dev "localhost"). It carries no
	// session (cookies are host-only), so it serves only public content.
	// Required.
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

	// TrustedProxies are CIDR ranges whose X-Forwarded-For / X-Real-IP headers are
	// trusted when resolving a request's client IP. Loopback is always trusted (a
	// same-host reverse proxy). The client IP is the rightmost X-Forwarded-For
	// entry that is NOT a trusted proxy; if the connecting peer itself isn't
	// trusted, forwarding headers are ignored entirely (so they can't be spoofed).
	// Configured via MESHTENDER_TRUSTED_PROXIES — a comma-separated list of CIDRs
	// or bare IPs, plus the shorthand token "private" (adds the RFC1918, link-local
	// and ULA ranges — handy when a home router/LAN sits in front).
	TrustedProxies []*net.IPNet
}

// RadioDefaults is a set of LoRa parameters, used to match a repeater's stored
// radio config against the region presets offered in the UI.
type RadioDefaults struct {
	FreqHz uint32
	BwHz   uint32
	SF     uint8
	CR     uint8
}

// Load reads configuration from the environment, applying defaults and
// validating required fields.
func Load() (*Config, error) {
	trustedProxies, err := parseTrustedProxies(os.Getenv("MESHTENDER_TRUSTED_PROXIES"))
	if err != nil {
		return nil, fmt.Errorf("MESHTENDER_TRUSTED_PROXIES: %w", err)
	}
	c := &Config{
		Addr:           envOr("MESHTENDER_ADDR", ":8080"),
		DatabaseURL:    os.Getenv("MESHTENDER_DATABASE_URL"),
		RPID:           envOr("MESHTENDER_RP_ID", "localhost"),
		RPDisplayName:  envOr("MESHTENDER_RP_NAME", "MeshTender"),
		RPOrigins:      splitOrigins(envOr("MESHTENDER_RP_ORIGIN", "http://localhost:8080")),
		PrimaryHost:    envOr("MESHTENDER_PRIMARY_HOST", envOr("MESHTENDER_RP_ID", "localhost")),
		AuthHost:       os.Getenv("MESHTENDER_AUTH_HOST"),
		RootHost:       os.Getenv("MESHTENDER_ROOT_HOST"),
		WWWHost:        os.Getenv("MESHTENDER_WWW_HOST"),
		TLSCert:        os.Getenv("MESHTENDER_TLS_CERT"),
		TLSKey:         os.Getenv("MESHTENDER_TLS_KEY"),
		TrustedProxies: trustedProxies,
	}

	// MeshTender runs across three hosts (auth + app + root). Require the two that
	// have no sane default (PrimaryHost falls back to RPID above).
	if c.AuthHost == "" {
		return nil, fmt.Errorf("MESHTENDER_AUTH_HOST is required")
	}
	if c.RootHost == "" {
		return nil, fmt.Errorf("MESHTENDER_ROOT_HOST is required")
	}
	if c.WWWHost == "" {
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

// privateRanges are added by the "private" shorthand token.
var privateRanges = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16",
	"fc00::/7", "fe80::/10",
}

// parseTrustedProxies parses MESHTENDER_TRUSTED_PROXIES into CIDR ranges. Loopback
// is always included (a same-host reverse proxy). Each entry may be a CIDR, a bare
// IP (treated as a /32 or /128), or the token "private"/"loopback". A malformed
// entry is a hard error: silently dropping it would leave the real proxy untrusted
// so its X-Forwarded-For is ignored, collapsing every client to the proxy's IP
// (spoof exposure and one shared rate-limit bucket).
func parseTrustedProxies(s string) ([]*net.IPNet, error) {
	nets := []*net.IPNet{mustCIDR("127.0.0.0/8"), mustCIDR("::1/128")}
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		switch strings.ToLower(tok) {
		case "loopback":
			continue // already included
		case "private":
			for _, c := range privateRanges {
				nets = append(nets, mustCIDR(c))
			}
			continue
		}
		if strings.Contains(tok, "/") {
			_, n, err := net.ParseCIDR(tok)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR %q: %w", tok, err)
			}
			nets = append(nets, n)
			continue
		}
		ip := net.ParseIP(tok)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP %q", tok)
		}
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		mask := net.CIDRMask(bits, bits)
		nets = append(nets, &net.IPNet{IP: ip.Mask(mask), Mask: mask})
	}
	return nets, nil
}

func mustCIDR(s string) *net.IPNet {
	_, n, _ := net.ParseCIDR(s)
	return n
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
