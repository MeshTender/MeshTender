package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
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
	"object-src 'none'",
	// form-action is NOT here: it has to name the sibling surfaces, and their origins
	// include the request's port, so it's built per request. See formAction.
}, "; ")

// formAction builds the form-action directive: 'self' plus the other two surface
// origins.
//
// 'self' alone is wrong here, and silently so. A credential POST lands on the auth
// host and answers 303 to the app host's handoff callback — and **Chrome enforces
// form-action across the redirect chain**, not just on the initial request (the CSP
// spec says it shouldn't, and Firefox doesn't, which is exactly why this survived).
//
// The symptom is why it went unnoticed for a month in production. The POST itself
// arrives normally — the handler runs, the account is created, the session is set, and
// the log shows a clean 303 — and then the browser refuses to follow that redirect,
// reporting it only to the console. To the user the button simply does nothing, so it
// reads as flakiness; to the server everything looks successful. Analytics for the
// affected window shows the tell: five sign-up POSTs in six minutes, all 303, from
// someone pressing a dead button.
//
// So this must list every surface a form on one host may redirect to. Origins are
// computed from the request, since dev serves all three on a non-default port and a
// source expression without a port only matches 443.
func (e *Env) formAction(r *http.Request) string {
	sources := []string{"'self'"}
	if e.Cfg != nil {
		for _, host := range []string{e.Cfg.PrimaryHost, e.Cfg.AuthHost, e.Cfg.RootHost} {
			if host == "" {
				continue
			}
			origin := originFor(e.Cfg, r, host)
			if !slices.Contains(sources, origin) {
				sources = append(sources, origin)
			}
		}
	}
	return "form-action " + strings.Join(sources, " ")
}

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
		policy := cspDirectives + "; " + e.formAction(r) +
			"; script-src 'self' 'nonce-" + nonce + "'"
		if e.csp != nil {
			// report-uri ONLY, deliberately — not the modern report-to /
			// Reporting-Endpoints pair, despite report-uri being deprecated.
			//
			// The trap is that these two don't compose: a browser that supports
			// report-to IGNORES report-uri when both are present. So advertising both
			// doesn't broaden coverage, it hands Chrome (the majority browser) over to
			// the report-to path exclusively. If that path doesn't deliver, Chrome
			// reports nothing at all — and an empty report table is indistinguishable
			// from a clean one, the worst failure mode a monitoring feature can have.
			//
			// TestCSPViolationIsReportedByARealBrowser measures which actually works.
			// With report-uri alone, Chrome delivers a genuine violation within a
			// second. With report-to advertised, it delivered nothing at all in the
			// same environment — explained by the self-signed certificate the e2e
			// suite serves (Security.setIgnoreCertificateErrors is scoped to the
			// inspected target and doesn't cover the network service's out-of-band
			// report uploads), which is a test artifact rather than a production one.
			//
			// That's the point: report-uri is verified end to end in the browser
			// engine we serve, and report-to is not verifiable here. Adding it would
			// trade a working path for an unverified one. Revisit once it can be
			// tested against a trusted certificate; parseCSPReports already handles
			// the Reporting-API wire format, so enabling it is a header change.
			policy += "; report-uri " + CSPReportPath
		}
		h.Set("Content-Security-Policy", policy)
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

// unsafeMethod reports whether a method can change server state. The safe set is
// closed (per RFC 9110), so anything unrecognized — including a method added by a
// future router — counts as unsafe and gets checked.
func unsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	}
	return true
}

// blockCrossSiteWrites rejects state-changing requests that the browser tells us
// came from another site. It is the second layer of CSRF defense: the first is the
// session cookie's SameSite=Lax, which browsers use to withhold the cookie from a
// cross-site POST. Lax is sound but it is a *single* control, and it is the wrong
// shape for two failure modes — a browser or embedded webview that mishandles
// SameSite re-opens every mutation, and a state-changing GET (easy to add by
// accident) is forgeable outright, because Lax deliberately permits top-level GET
// navigation.
//
// Sec-Fetch-Site is set by the browser and cannot be forged by page JavaScript (it
// is a forbidden header name), so it is trustworthy when present. Values are
// handled as follows:
//
//   - "cross-site" — rejected. This is the CSRF case: an attacker's page driving a
//     write against us.
//   - "same-origin" / "same-site" — allowed. Every form action and fetch() in the
//     app is a relative path, and the CSP's form-action allows only this app's own
//     surfaces (see formAction), so real writes are same-origin or between our own
//     hosts. "same-site" is also allowed because it is exactly
//     what Lax cookies already permit (sibling subdomains), so rejecting it would
//     buy nothing this doesn't already concede.
//   - "none" — allowed. It means the user initiated the request directly (address
//     bar, bookmark), which an attacker cannot arrange; rejecting it would add no
//     security and risk odd client behavior.
//   - missing / unrecognized — allowed. Pre-2020 browsers and non-browser clients
//     send nothing, and this is defense in depth layered on SameSite, not a
//     replacement for it. Failing closed here would break those clients for no
//     gain against an attacker, who cannot suppress the header in a real browser.
//
// Rejections are logged at Warn: this is a new control, and if it ever fires on
// legitimate traffic we want to find out from the logs rather than a bug report.
//
// Note this is a header check by design, which is what makes it cheap. The
// alternative — a synchronizer token in every form — would put a secret in an HTML
// body, and HTML is compressed (see compressHTML), which is the BREACH
// precondition. Keeping the check in headers sidesteps that entirely.
//
// The violation-report endpoint is exempt. Reports are POSTs the browser generates
// itself, out-of-band from the document that triggered them, and the Sec-Fetch-Site
// value on that delivery is not something the CSP or Reporting API specifications
// pin down — so a report could arrive labeled "cross-site" and be silently
// discarded, leaving reporting looking merely quiet. Exempting it costs nothing:
// the endpoint takes no session, performs no authenticated action, and its only
// effect is incrementing a counter on an aggregate row (see CSPCollector).
func blockCrossSiteWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == CSPReportPath {
			next.ServeHTTP(w, r)
			return
		}
		if unsafeMethod(r.Method) && strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
			slog.Warn("blocked cross-site write",
				"method", r.Method,
				"path", r.URL.Path,
				"host", r.Host,
				"request_id", middleware.GetReqID(r.Context()),
				"client_ip", clientIP(r),
			)
			http.Error(w, "This request appears to have come from another site and was blocked.",
				http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NoStore marks a response as never-cacheable. Without it a response carrying no
// Cache-Control and no Expires is *heuristically* cacheable, so the browser's
// back button re-renders a signed-in page after sign-out — on a shared machine
// that hands the next person the previous user's dashboard, repeater list and
// command log. scs's Vary: Cookie stops a shared cache cross-serving one user's
// page to another, so this closes the local/history exposure that Vary can't.
//
// Applied to the session-bearing route groups (see each surface's sessionMW), not
// blanket per host: static assets and /healthz are registered ahead of the session
// middleware and must keep their own immutable caching. Anything outside those
// groups has no session, so it can't render user data in the first place.
//
// Note this also opts those pages out of the back/forward cache — bfcache is
// disabled by no-store specifically (no-cache would not do it) — so back
// navigation is a real request rather than an instant restore. That's the point.
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
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
