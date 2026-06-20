package auth

import "testing"

func TestSafeLocalPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Allowed: rooted, same-origin paths.
		{"/", true},
		{"/dashboard", true},
		{"/repeaters/abc/console", true},
		{"/orgs/foo?view=public", true},
		{"/a/b/c", true},

		// Rejected: not rooted.
		{"", false},
		{"dashboard", false},
		{"http://evil.com", false},
		{"https://evil.com", false},

		// Rejected: protocol-relative and the backslash bypass that browsers
		// normalize to "//evil.com".
		{"//evil.com", false},
		{"/\\evil.com", false},
		{"/\\/evil.com", false},

		// Rejected: control characters.
		{"/foo\nLocation: bar", false},
		{"/foo\tbar", false},
		{"/foo\x00", false},
	}
	for _, c := range cases {
		if got := SafeLocalPath(c.path); got != c.want {
			t.Errorf("SafeLocalPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
