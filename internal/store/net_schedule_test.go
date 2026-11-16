package store

import (
	"testing"
	"time"
)

// TestNetSchedule: a net's recurrence reads in the same 12-hour style as every
// timestamp, naming AM/PM once when the session stays on one side of noon, and
// the zone as people say it.
func TestNetSchedule(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		start, minutes int
		tz, want       string
	}{
		{19 * 60, 60, "America/New_York", "Sundays, 7:00–8:00 PM, New York time"},
		{11*60 + 30, 60, "America/Los_Angeles", "Sundays, 11:30 AM–12:30 PM, Los Angeles time"},
		{23 * 60, 120, "UTC", "Sundays, 11:00 PM–1:00 AM, UTC"},
	} {
		in := NetInput{Weekday: time.Sunday, StartMinute: tc.start, DurationMinutes: tc.minutes, Timezone: tc.tz}
		if got := in.Schedule(); got != tc.want {
			t.Errorf("Schedule(%d+%d %s) = %q, want %q", tc.start, tc.minutes, tc.tz, got, tc.want)
		}
	}
}
