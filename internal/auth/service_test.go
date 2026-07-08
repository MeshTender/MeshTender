package auth

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestValidPassword pins the 8-char floor and confirms there is no upper bound:
// pre-hashing removes bcrypt's 72-byte input limit, so long passwords are valid.
func TestValidPassword(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int
		want bool
	}{
		{7, false}, {8, true}, {72, true}, {73, true}, {5000, true},
	}
	for _, c := range cases {
		if got := ValidPassword(strings.Repeat("a", c.n)); got != c.want {
			t.Errorf("ValidPassword(len=%d) = %v, want %v", c.n, got, c.want)
		}
	}
}

// TestPasswordHashRoundTrip: a password far past bcrypt's 72-byte input limit
// hashes and verifies, and — crucially — two passwords that differ only after
// byte 72 do NOT collide (raw bcrypt would treat them as equal; pre-hashing is
// exactly what prevents that).
func TestPasswordHashRoundTrip(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("correct horse battery staple ", 10) // ~290 bytes
	hash, err := hashPassword(long)
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if ok, legacy := comparePassword(hash, long); !ok || legacy {
		t.Fatalf("comparePassword(correct) = (%v, %v), want (true, false)", ok, legacy)
	}
	if ok, _ := comparePassword(hash, long+"x"); ok {
		t.Fatal("comparePassword accepted a wrong password")
	}

	a, _ := hashPassword(strings.Repeat("a", 72) + "1")
	if ok, _ := comparePassword(a, strings.Repeat("a", 72)+"2"); ok {
		t.Fatal("passwords differing only after byte 72 collided (pre-hash not applied?)")
	}
}

// TestComparePasswordLegacy: a hash written the old way (raw bcrypt of the
// password) still verifies and is flagged legacy so the caller can upgrade it.
func TestComparePasswordLegacy(t *testing.T) {
	t.Parallel()
	const pw = "legacy-secret"
	raw, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	if ok, legacy := comparePassword(string(raw), pw); !ok || !legacy {
		t.Fatalf("legacy compare = (%v, %v), want (true, true)", ok, legacy)
	}
	if ok, _ := comparePassword(string(raw), "wrong"); ok {
		t.Fatal("legacy compare accepted a wrong password")
	}
}

func TestSafeLocalPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want bool
	}{
		// Allowed: rooted, same-origin paths.
		{"/", true},
		{"/dashboard", true},
		{"/repeaters/abc/console", true},
		{"/orgs/foo?sort=name", true},
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
