package core

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jleight/meshtender/internal/store"
)

// cmd builds a catalog entry; resolveCommand only reads Template and Arity, and
// Key is carried for readable assertions.
func cmd(key, template string, arity int) *store.Command {
	return &store.Command{Key: key, Template: template, Arity: arity}
}

// resolveTestCatalog mirrors the real catalog's tricky cases: overloaded tokens
// (setperm, region put), prefix-related tokens (region vs region get/put),
// dotted sub-keys (set flood.max vs set flood.max.advert), comma-arg commands
// (set radio = one token), and variadic free-text (set name).
func resolveTestCatalog() []*store.Command {
	return []*store.Command{
		cmd("ver", "ver", 0),
		cmd("advert", "advert", 0),
		cmd("advert.zerohop", "advert.zerohop", 0),
		cmd("erase", "erase", 0),
		cmd("setperm.set", "setperm <pubkey> <level>", 2),
		cmd("setperm.remove", "setperm <pubkey>", 1),
		cmd("set.tx", "set tx <0-22>", 1),
		cmd("set.radio", "set radio <f,bw,sf,cr>", 1),
		cmd("set.name", "set name <text>", arityVariadic),
		cmd("set.flood_max", "set flood.max <n>", 1),
		cmd("set.flood_max_advert", "set flood.max.advert <n>", 1),
		cmd("region", "region", 0),
		cmd("region.get", "region get <name>", 1),
		cmd("region.put_root", "region put <name>", 1),
		cmd("region.put_sub", "region put <name> <parent>", 2),
		cmd("region.def", "region def <tokens>", arityVariadic),
		cmd("region.home_get", "region home", 0),
		cmd("region.home_set", "region home <name>", 1),
	}
}

func TestResolveCommand(t *testing.T) {
	cat := resolveTestCatalog()
	cases := []struct {
		typed, want string // want == "" means must be denied (nil)
	}{
		// Exact no-arg commands; extra tokens must NOT be swallowed.
		{"ver", "ver"},
		{"ver extra", ""},
		{"advert", "advert"},
		{"advert.zerohop", "advert.zerohop"},
		{"advert evil", ""}, // no args allowed on a 0-arity command
		{"erase", "erase"},

		// setperm overloaded by arity: 2 = set, 1 = remove, anything else denied.
		{"setperm KEY 3", "setperm.set"},
		{"setperm KEY", "setperm.remove"},
		{"setperm KEY 3 junk", ""},
		{"setperm", ""},

		// Single-arg vs missing/extra args.
		{"set tx 20", "set.tx"},
		{"set tx", ""},
		{"set tx 20 30", ""},

		// Comma-arg radio is ONE whitespace token (arity 1); space form is denied
		// (the firmware parses it comma-separated, so the space form is invalid).
		{"set radio 868,250,11,5", "set.radio"},
		{"set radio 868 250 11 5", ""},

		// Variadic free-text takes any number of trailing tokens.
		{"set name My Cool Repeater", "set.name"},
		{"set name", "set.name"},

		// Dotted sub-keys are distinct words — no prefix collision.
		{"set flood.max 5", "set.flood_max"},
		{"set flood.max.advert 5", "set.flood_max_advert"},

		// region: bare is a 0-arg read; "region <unknown>" must be DENIED rather
		// than falling through to the bare read (the catch-all bug this prevents).
		{"region", "region"},
		{"region foo", ""},
		{"region get EU", "region.get"},
		{"region get", ""},

		// Longest token wins: "region put" over "region"; arity picks root vs sub.
		{"region put EU", "region.put_root"},
		{"region put EU NA", "region.put_sub"},
		{"region put EU NA X", ""},
		{"region def A B C", "region.def"},

		// Optional-arg get/set split by arity.
		{"region home", "region.home_get"},
		{"region home EU", "region.home_set"},

		// Whitespace is normalized.
		{"setperm   KEY   3", "setperm.set"},
		{"  set tx 20  ", "set.tx"},

		// Total nonsense.
		{"totally bogus", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := resolveCommand(tc.typed, cat)
		gotKey := ""
		if got != nil {
			gotKey = got.Key
		}
		if gotKey != tc.want {
			t.Errorf("resolveCommand(%q) = %q, want %q", tc.typed, gotKey, tc.want)
		}
	}
}

func TestValidCommandText(t *testing.T) {
	ok := []string{"ver", "set tx 20", "set name My Repeater", "setperm abc 3"}
	for _, s := range ok {
		if !validCommandText(s) {
			t.Errorf("validCommandText(%q) = false, want true", s)
		}
	}
	bad := []string{
		"",
		"   ",
		"ver\nreboot",     // newline (chaining attempt)
		"ver\rreboot",     // carriage return
		"set name a\x00b", // null
		"cmd\x7f",         // delete
		strings.Repeat("a", maxCommandLen+1), // too long
	}
	for _, s := range bad {
		if validCommandText(s) {
			t.Errorf("validCommandText(%q) = true, want false", s)
		}
	}
}

// TestCommandFeatureCoverage ensures every catalog command carries a feature and
// a valid operation, and that its feature is listed in featureOrder so it renders
// in a known position (a feature missing from featureOrder still shows, but at
// the end — this catches the Go list drifting from the DB). Requires the *_test
// database. The DB also enforces feature<>'' and operation IN (...) via CHECK.
func TestCommandFeatureCoverage(t *testing.T) {
	cat := loadRealCatalog(t)
	validOp := map[string]bool{"read": true, "write": true, "delete": true, "action": true}
	for _, c := range cat {
		if c.Feature == "" {
			t.Errorf("command %q has no feature", c.Key)
		} else if featureRank(c.Feature) == len(featureOrder) {
			t.Errorf("command %q feature %q is not in featureOrder", c.Key, c.Feature)
		}
		if !validOp[c.Operation] {
			t.Errorf("command %q has invalid operation %q", c.Key, c.Operation)
		}
	}
}

// TestResolveCommandRealCatalog enforces, against the actual seeded catalog, the
// two invariants the console parser's safety depends on:
//  1. No two commands share a (token, arity) — resolution is never ambiguous.
//  2. Every fixed-arity command's arity equals its template's "<arg>" count, and
//     each command's own template round-trips back to itself through the parser.
//
// loadRealCatalog migrates the *_test database and returns the seeded catalog,
// skipping the test when the DB isn't configured.
func loadRealCatalog(t *testing.T) []*store.Command {
	t.Helper()
	dsn := os.Getenv("MESHTENDER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set MESHTENDER_TEST_DATABASE_URL to run this integration test")
	}
	if u, err := url.Parse(dsn); err != nil || !strings.HasSuffix(strings.TrimPrefix(u.Path, "/"), "_test") {
		t.Fatalf("refusing to run: test DB name must end in _test (got %q)", dsn)
	}
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cat, err := st.ListCommands(ctx)
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(cat) == 0 {
		t.Fatal("empty catalog")
	}
	return cat
}

// Requires the *_test database (it runs migrations).
func TestResolveCommandRealCatalog(t *testing.T) {
	cat := loadRealCatalog(t)
	argGroup := regexp.MustCompile(`<[^>]*>`)
	seen := map[string]string{} // (token|arity) -> key
	for _, c := range cat {
		token := commandPrefix(c.Template)
		groups := len(argGroup.FindAllString(c.Template, -1))

		// (1) uniqueness of (token, arity)
		k := fmt.Sprintf("%q|%d", token, c.Arity)
		if other, dup := seen[k]; dup {
			t.Errorf("commands %q and %q share token %q + arity %d (ambiguous)", other, c.Key, token, c.Arity)
		}
		seen[k] = c.Key

		// (2) fixed arity must match the template's arg-group count
		if c.Arity >= 0 && groups != c.Arity {
			t.Errorf("command %q: arity %d but template %q has %d <arg> groups", c.Key, c.Arity, c.Template, groups)
		}

		// (3) the command's own template round-trips back to itself
		n := c.Arity
		if n < 0 {
			n = 1 // variadic: one sample arg
		}
		sample := token
		for i := 0; i < n; i++ {
			sample += " x"
		}
		got := resolveCommand(sample, cat)
		if got == nil || got.Key != c.Key {
			gk := "<nil>"
			if got != nil {
				gk = got.Key
			}
			t.Errorf("round-trip: %q (for %q) resolved to %q", sample, c.Key, gk)
		}
	}
}
