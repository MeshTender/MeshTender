package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
)

// nonceCtxKey keys the per-request CSP nonce in the request context.
type nonceCtxKey struct{}

// cspDirectives is the Content-Security-Policy, minus the per-request script
// nonce which is appended at request time. Scripts are locked to same-origin plus
// a per-request nonce (so inline scripts must be nonce'd and inline on*= handlers
// are rejected — the guard against injected script). Styles keep 'unsafe-inline'
// (low XSS risk, and avoids nonce'ing every style= attribute + htmx's indicator
// style). The only external resource is CARTO map tiles (images).
var cspDirectives = strings.Join([]string{
	"default-src 'self'",
	"style-src 'self' 'unsafe-inline'",
	"img-src 'self' data: https://*.basemaps.cartocdn.com",
	"connect-src 'self'",
	"font-src 'self' data:",
	"frame-ancestors 'none'",
	"base-uri 'self'",
	"form-action 'self'",
	"object-src 'none'",
}, "; ")

// permissionsPolicy denies powerful browser features we don't use and explicitly
// allows the two we do: WebSerial (the KISS modem on the confirm/console pages)
// and WebAuthn (passkeys). Unknown tokens are ignored by browsers that don't
// implement them.
const permissionsPolicy = "serial=(self), " +
	"publickey-credentials-get=(self), publickey-credentials-create=(self), " +
	"geolocation=(), camera=(), microphone=(), payment=(), usb=()"

// securityHeaders sets a strict Content-Security-Policy (with a fresh per-request
// script nonce, also exposed via the context for templates) plus companion
// hardening headers, on every response.
func (e *Env) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := newNonce()
		h := w.Header()
		h.Set("Content-Security-Policy", cspDirectives+"; script-src 'self' 'nonce-"+nonce+"'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY") // old-browser fallback for frame-ancestors 'none'
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", permissionsPolicy)
		// HSTS only over TLS — never on plain-http localhost dev, where it would
		// pin the browser to https for the dev host.
		if e.Cfg != nil && e.Cfg.Secure {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nonceCtxKey{}, nonce)))
	})
}

// NonceFromContext returns the per-request CSP script nonce, or "" if unset.
func NonceFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(nonceCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// newNonce returns a fresh base64 CSP nonce (16 random bytes).
func newNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is fatal for security; a static fallback would defeat
		// the nonce, so panic (matches how the app treats other rand failures).
		panic("csp nonce: " + err.Error())
	}
	return base64.RawStdEncoding.EncodeToString(b[:])
}
