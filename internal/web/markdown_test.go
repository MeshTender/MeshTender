package web

import "testing"

func TestMarkdownText(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"empty":     {"", ""},
		"emphasis":  {"We run **many** repeaters.", "We run many repeaters."},
		"heading":   {"# Buffalo Mesh\n\nCovering the whole area.", "Buffalo Mesh Covering the whole area."},
		"list":      {"Gear:\n\n- radios\n- antennas", "Gear: radios antennas"},
		"link":      {"See [our site](https://example.com) for more.", "See our site for more."},
		"collapses": {"line one\n\n\nline two", "line one line two"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := MarkdownText(c.in); got != c.want {
				t.Fatalf("MarkdownText(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
