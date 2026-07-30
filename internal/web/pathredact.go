package web

import "strings"

// RedactPath replaces a secret path segment with a placeholder before a request
// path is recorded anywhere durable, so a share-link/invite token — a live secret
// until the invite is accepted — doesn't sit in an analytics or CSP-report table
// for the retention window. The invite token is the only secret carried in a URL
// *path*; the login-handoff codes travel in the query string, which no recorder
// stores.
//
// Templatizing (rather than hashing) also keeps aggregates meaningful: every invite
// hit rolls up under one path.
//
// It lives in web rather than in either recorder because both the analytics
// middleware and the CSP report endpoint need exactly this guarantee, and web is the
// package they both already depend on.
func RedactPath(p string) string {
	rest, ok := strings.CutPrefix(p, "/invite/")
	if !ok || rest == "" {
		return p
	}
	// /invite/{token} → /invite/:token; /invite/{token}/accept keeps the tail.
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return "/invite/:token" + rest[i:]
	}
	return "/invite/:token"
}
