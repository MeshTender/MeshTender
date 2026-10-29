package web

import (
	"fmt"
	"time"
	"unicode/utf8"
)

// Clip trims a string to at most n bytes, backing off to a valid UTF-8 boundary
// if the cut landed mid-rune. Use it to bound stored/searched text from form
// input: a plain s[:n] can split a multi-byte rune, producing invalid UTF-8 that
// Postgres rejects (surfacing to the user as an unexplained save failure).
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// HumanDuration renders a duration the way copy should read it, in its largest
// whole unit ("3 days", "24 hours", "45 minutes"), so stated times can't drift
// from the constants behind them. Days only from two of them on: "24 hours"
// reads better than "1 day" for a link's shelf life.
func HumanDuration(d time.Duration) string {
	n, unit := int64(d/time.Minute), "minute"
	switch {
	case d >= 48*time.Hour:
		n, unit = int64(d/(24*time.Hour)), "day"
	case d >= time.Hour:
		n, unit = int64(d/time.Hour), "hour"
	}
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
