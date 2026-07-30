package web

import "testing"

// TestRedactPath: the secret invite/share token is templatized so it never reaches
// a durable record — the analytics events table or a CSP report row; non-invite
// paths pass through untouched. Regression for the pre-release audit finding that
// live tokens were recorded.
//
// Moved here from internal/analytics when the CSP report endpoint became a second
// caller: both record request paths, and both must strip the same secret.
func TestRedactPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/invite/abc123secret", "/invite/:token"},
		{"/invite/abc123secret/accept", "/invite/:token/accept"},
		{"/invite/", "/invite/"}, // no token, nothing to redact
		{"/invite", "/invite"},
		{"/dashboard", "/dashboard"},
		{"/r/pub-id-not-secret", "/r/pub-id-not-secret"},
		{"/orgs/some-slug", "/orgs/some-slug"},
	}
	for _, c := range cases {
		if got := RedactPath(c.in); got != c.want {
			t.Errorf("RedactPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
