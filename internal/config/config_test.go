package config

import (
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
