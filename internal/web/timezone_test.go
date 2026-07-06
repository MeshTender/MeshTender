package web

import (
	"strings"
	"testing"
	"time"
)

func TestValidTimeZone(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"", true},                        // auto-detect
		{"UTC", true},                     // always present
		{"America/New_York", true},        // canonical zone
		{"Australia/Sydney", true},        // canonical zone
		{"Local", false},                  // the server's own zone — not a user preference
		{"Mars/Olympus_Mons", false},      // not a zone
		{"'; DROP TABLE users;--", false}, // junk
	}
	for _, c := range cases {
		if got := ValidTimeZone(c.name); got != c.want {
			t.Errorf("ValidTimeZone(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTimeElement(t *testing.T) {
	// Zero time renders nothing so call sites can guard on presence.
	if got := TimeElement(time.Time{}, "datetime"); got != "" {
		t.Errorf("zero time = %q, want empty", got)
	}

	// A known instant renders a machine-readable UTC datetime attr, the kind,
	// and a UTC-labeled fallback.
	ts := time.Date(2026, 7, 5, 15, 4, 5, 0, time.FixedZone("EST", -5*3600))
	got := string(TimeElement(ts, "datetime"))
	for _, want := range []string{
		`datetime="2026-07-05T20:04:05Z"`, // converted to UTC (15:04 EST → 20:04 UTC)
		`data-fmt="datetime"`,
		`Jul 5, 2026, 20:04 UTC`,
		`</time>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("TimeElement datetime = %q, missing %q", got, want)
		}
	}

	// An unknown kind falls back to datetime rather than emitting a broken attr.
	if fb := string(TimeElement(ts, "bogus")); !strings.Contains(fb, `data-fmt="datetime"`) {
		t.Errorf("unknown kind = %q, want datetime fallback", fb)
	}

	// date/time kinds carry their layout.
	if d := string(TimeElement(ts, "date")); !strings.Contains(d, `data-fmt="date"`) || !strings.Contains(d, "Jul 5, 2026") {
		t.Errorf("date kind = %q", d)
	}
	if s := string(TimeElement(ts, "time-seconds")); !strings.Contains(s, "20:04:05 UTC") {
		t.Errorf("time-seconds kind = %q", s)
	}
}
