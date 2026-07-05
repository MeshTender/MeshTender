package web

import "unicode/utf8"

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
