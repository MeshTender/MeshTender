package config

import (
	"net"
	"strings"
	"testing"
)

// validEnv sets every variable Load needs for a successful split-host load. Tests
// override individual vars afterward. t.Setenv fully controls the environment
// (overriding any ambient .env that `mise` may have sourced) and restores it.
func validEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MESHTENDER_DATABASE_URL", "postgres://x/y")
	t.Setenv("MESHTENDER_MASTER_KEY", strings.Repeat("00", 32))
	t.Setenv("MESHTENDER_RP_ID", "example.dev")
	t.Setenv("MESHTENDER_RP_ORIGIN", "https://auth.example.dev,https://app.example.dev")
	t.Setenv("MESHTENDER_PRIMARY_HOST", "app.example.dev")
	t.Setenv("MESHTENDER_AUTH_HOST", "auth.example.dev")
	t.Setenv("MESHTENDER_ROOT_HOST", "example.dev")
	t.Setenv("MESHTENDER_WWW_HOST", "")
	// Blanked explicitly so an ambient .env can't switch mail on underneath a test
	// that says nothing about it.
	t.Setenv("MESHTENDER_RESEND_API_KEY", "")
	t.Setenv("MESHTENDER_MAIL_FROM", "")
	t.Setenv("MESHTENDER_MAIL_REPLY_TO", "")
}

// TestLoadMailDisabledByDefault: with nothing configured there's no from-address, so
// the recovery-by-email UI stays hidden rather than offering mail that can't be sent.
func TestLoadMailDisabledByDefault(t *testing.T) {
	validEnv(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.MailEnabled {
		t.Fatal("MailEnabled = true with nothing configured")
	}
}

// TestLoadMailEnabledWithoutAPIKey is the dev path, and the reason the feature switch
// and the delivery switch are separate: MAIL_FROM alone turns the recovery UI on
// while messages go to the log. If this required an API key, none of the email UI
// would be reachable locally and the logging sender would be useless.
func TestLoadMailEnabledWithoutAPIKey(t *testing.T) {
	validEnv(t)
	t.Setenv("MESHTENDER_MAIL_FROM", "MeshTender <noreply@example.dev>")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.MailEnabled {
		t.Error("MailEnabled = false with a from-address set")
	}
	if c.ResendAPIKey != "" {
		t.Error("an API key appeared from nowhere")
	}
}

// TestLoadMailKeyWithoutFromFailsFast: an API key with no From address can never
// deliver, and the only symptom would be recovery mail silently never arriving —
// so it's a startup error, not a warning.
func TestLoadMailKeyWithoutFromFailsFast(t *testing.T) {
	validEnv(t)
	t.Setenv("MESHTENDER_RESEND_API_KEY", "re_live_key")
	if _, err := Load(); err == nil {
		t.Fatal("Load succeeded with an API key and no MESHTENDER_MAIL_FROM, want an error")
	}
}

// TestLoadMailEnabled: both halves present ⇒ mail is on and delivers for real.
func TestLoadMailEnabled(t *testing.T) {
	validEnv(t)
	t.Setenv("MESHTENDER_RESEND_API_KEY", "re_live_key")
	t.Setenv("MESHTENDER_MAIL_FROM", "MeshTender <noreply@example.dev>")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.MailEnabled {
		t.Fatal("MailEnabled = false with both key and from set")
	}
}

func TestLoadSplitHost(t *testing.T) {
	validEnv(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AuthHost != "auth.example.dev" || c.RootHost != "example.dev" || c.PrimaryHost != "app.example.dev" {
		t.Fatalf("hosts = %q/%q/%q", c.AuthHost, c.RootHost, c.PrimaryHost)
	}
	// WWWHost defaults to www.<RootHost> when unset.
	if c.WWWHost != "www.example.dev" {
		t.Fatalf("WWWHost = %q, want www.example.dev", c.WWWHost)
	}
	// https origin ⇒ Secure.
	if !c.Secure {
		t.Fatal("Secure = false for https origins")
	}
}

// TestLoadTrustedProxiesTypoFailsFast: a malformed MESHTENDER_TRUSTED_PROXIES
// entry must fail the load, not be silently dropped — a dropped entry leaves the
// real proxy untrusted, so its X-Forwarded-For is ignored and every client
// collapses to the proxy's IP (one shared rate-limit bucket).
func TestLoadTrustedProxiesTypoFailsFast(t *testing.T) {
	for _, bad := range []string{"10.0.0.0/33", "10.0.0.0/", "not-an-ip", "10.0.0.256", "10.0.0.0/8, bogus"} {
		t.Run(bad, func(t *testing.T) {
			validEnv(t)
			t.Setenv("MESHTENDER_TRUSTED_PROXIES", bad)
			if _, err := Load(); err == nil {
				t.Fatalf("Load succeeded with malformed MESHTENDER_TRUSTED_PROXIES=%q, want an error", bad)
			}
		})
	}
}

// TestLoadTrustedProxiesValid: well-formed entries (CIDR, bare IP, keyword) parse,
// and loopback is always present.
func TestLoadTrustedProxiesValid(t *testing.T) {
	validEnv(t)
	t.Setenv("MESHTENDER_TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.5, private, ::1")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Loopback (127.0.0.0/8) is always included, plus the four supplied ranges
	// (private expands to the private v4/v6 ranges, so just assert loopback + the
	// explicit ones resolve).
	mustMatch := func(ip string, want bool) {
		t.Helper()
		parsed := net.ParseIP(ip)
		got := false
		for _, n := range c.TrustedProxies {
			if n.Contains(parsed) {
				got = true
				break
			}
		}
		if got != want {
			t.Errorf("trusted contains %s = %v, want %v", ip, got, want)
		}
	}
	mustMatch("127.0.0.1", true)   // always-on loopback
	mustMatch("10.1.2.3", true)    // 10.0.0.0/8
	mustMatch("192.168.1.5", true) // bare IP → /32
	mustMatch("8.8.8.8", false)    // not trusted
}

func TestLoadRequiresAuthAndRootHost(t *testing.T) {
	// AUTH_HOST and ROOT_HOST are required; Load must fail fast when either is
	// missing rather than silently falling back.
	for _, missing := range []string{"MESHTENDER_AUTH_HOST", "MESHTENDER_ROOT_HOST"} {
		t.Run(missing, func(t *testing.T) {
			validEnv(t)
			t.Setenv(missing, "")
			_, err := Load()
			if err == nil {
				t.Fatalf("Load succeeded with %s unset, want an error", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Fatalf("error %q does not name %s", err, missing)
			}
		})
	}
}
