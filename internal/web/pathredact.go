package web

import "strings"

// secretPathPrefixes are the URL path prefixes whose next segment is a live
// credential: a share-link/invite token, an email-confirmation token, or a
// password-reset token. Each is a secret only until it's redeemed, and each arrives
// as a plain GET that the analytics middleware and the CSP report endpoint would
// otherwise record verbatim — parking a working token in a table for the whole
// retention window. A reset token is the most dangerous of the three, since it
// authorizes taking over an account.
//
// The login-handoff codes need no entry here: they travel in the query string, which
// no recorder stores.
var secretPathPrefixes = []string{"/invite/", "/verify-email/", "/reset/"}

// RedactPath replaces a secret path segment with a placeholder before a request path
// is recorded anywhere durable.
//
// Templatizing (rather than hashing) also keeps aggregates meaningful: every invite
// hit rolls up under one path.
//
// It lives in web rather than in either recorder because both the analytics
// middleware and the CSP report endpoint need exactly this guarantee, and web is the
// package they both already depend on.
func RedactPath(p string) string {
	for _, prefix := range secretPathPrefixes {
		rest, ok := strings.CutPrefix(p, prefix)
		if !ok || rest == "" {
			continue
		}
		// /invite/{token} → /invite/:token; /invite/{token}/accept keeps the tail.
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			return prefix + ":token" + rest[i:]
		}
		return prefix + ":token"
	}
	return p
}
