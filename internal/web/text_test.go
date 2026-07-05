package web

import (
	"testing"
	"unicode/utf8"
)

func TestClip(t *testing.T) {
	t.Parallel()
	// "€" is 3 bytes (E2 82 AC); placing it across the byte boundary is the case a
	// naive s[:n] would corrupt.
	cases := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"shorter than limit", "hello", 100, "hello"},
		{"exactly at limit", "hello", 5, "hello"},
		{"ascii truncated", "hello", 3, "hel"},
		{"zero limit", "hello", 0, ""},
		{"cut mid-rune backs off", "aaa€", 4, "aaa"},   // byte 4 is mid-€ → drop it
		{"cut mid-rune backs off 2", "aaa€", 5, "aaa"}, // byte 5 still mid-€
		{"rune fits exactly", "aaa€", 6, "aaa€"},       // "aaa"=3 + "€"=3
		{"multibyte fully kept", "café", 100, "café"},
	}
	for _, c := range cases {
		got := Clip(c.s, c.n)
		if got != c.want {
			t.Errorf("%s: Clip(%q, %d) = %q, want %q", c.name, c.s, c.n, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("%s: Clip(%q, %d) = %q is not valid UTF-8", c.name, c.s, c.n, got)
		}
		if len(got) > c.n {
			t.Errorf("%s: Clip(%q, %d) = %q exceeds %d bytes", c.name, c.s, c.n, got, c.n)
		}
	}
}
