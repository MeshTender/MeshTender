// Package web is the shared HTTP foundation: template rendering, common
// middleware/helpers, and the host dispatcher that the marketing/auth/core
// surface packages build on. It deliberately does NOT import the surface
// packages (or internal/auth), so those can import web without a cycle.
package web

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/identity"
	"github.com/jleight/meshtender/internal/store"
)

//go:embed templates/base.html templates/icons.html templates/org_tabs.html templates/repeater_tabs.html templates/command_grid.html templates/org_access.html templates/org_public.html templates/org_config.html templates/org_repeaters.html templates/error.html
var sharedTemplatesFS embed.FS

// sharedPages are full content pages (not just layout partials) that more than
// one surface renders. They're composed onto the base layout for every surface,
// so the root host (anonymous) and the app host (signed-in) can render the same
// public org page without duplicating the template.
var sharedPages = []string{"templates/org_public.html", "templates/org_config.html", "templates/org_repeaters.html", "templates/error.html"}

//go:embed static/*
var staticFS embed.FS

// UserInfoFunc reports the signed-in user's display name, admin flag, and
// preferred IANA time zone (empty = auto-detect) for the page chrome and
// timestamp localization. ok is false when no user is signed in. Injected by the
// assembler (which wires it from the auth service + store) so web stays auth-free.
type UserInfoFunc func(ctx context.Context) (name string, canAdmin bool, tz string, ok bool)

// Deps are the shared dependencies every surface needs.
type Deps struct {
	Store     *store.Store
	Identity  *identity.Service
	Cfg       *config.Config
	UserInfo  UserInfoFunc
	LookupTXT func(name string) ([]string, error)
}

// Env is the shared environment a surface's Handlers embeds. It carries the
// store/identity/config, the surface's renderer, and the DNS lookup.
type Env struct {
	Store    *store.Store
	Identity *identity.Service
	Cfg      *config.Config
	Renderer *Renderer
	// LookupTXT resolves DNS TXT records; injectable so domain verification is
	// testable. Defaults to net.LookupTXT.
	LookupTXT func(name string) ([]string, error)
}

// NewEnv builds a surface environment from shared Deps plus that surface's own
// page templates (composed onto the shared base layout).
func NewEnv(d Deps, surfaceTemplates fs.FS) (*Env, error) {
	r, err := NewRenderer(d.Cfg, surfaceTemplates)
	if err != nil {
		return nil, err
	}
	r.userInfo = d.UserInfo
	lookup := d.LookupTXT
	if lookup == nil {
		lookup = net.LookupTXT
	}
	return &Env{Store: d.Store, Identity: d.Identity, Cfg: d.Cfg, Renderer: r, LookupTXT: lookup}, nil
}

// Render delegates to the shared renderer (convenience for handlers via Env).
func (e *Env) Render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	e.Renderer.Render(w, r, page, data)
}

// ServerError logs an internal failure (keyed by request ID, so a report can be
// traced to its cause) and renders the branded 500 page with userMsg as the
// message. Use it at every handler site that currently drops an err on the floor:
// the visitor still sees only userMsg, but the real cause is recoverable from the
// logs. For expected client errors (4xx) keep using http.Error directly — those
// aren't server faults and shouldn't log at error level.
func (e *Env) ServerError(w http.ResponseWriter, r *http.Request, userMsg string, err error) {
	LogError(r, userMsg, err)
	e.ErrorPage(w, r, http.StatusInternalServerError, "Something went wrong", userMsg)
}

// NotFound renders the branded 404 page. It matches http.HandlerFunc so it works
// both as a chi NotFound handler (unrouted paths) and at handler call sites where
// a requested resource doesn't exist.
func (e *Env) NotFound(w http.ResponseWriter, r *http.Request) {
	e.ErrorPage(w, r, http.StatusNotFound, "Page not found",
		"We couldn't find that page. It may have moved, or the link may be wrong.")
}

// ErrorPage renders the shared branded error page (error.html) with the given
// status, on the surface's default layout so the chrome matches the host.
func (e *Env) ErrorPage(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	e.Renderer.render(w, r, status, "error.html", map[string]any{
		"Status": status, "Title": title, "Message": message,
	})
}

// LogError emits a structured error log for a failed request, keyed by request
// ID so it can be correlated with a user report. It writes no response — use it
// for failures that surface to the client through another channel (a WebSocket
// status frame, a redirect flash) or that have no response at all. Extra
// slog key/value pairs can be appended.
func LogError(r *http.Request, msg string, err error, args ...any) {
	base := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"request_id", middleware.GetReqID(r.Context()),
		"err", err,
	}
	slog.Error(msg, append(base, args...)...)
}

// LogAudit records a security-relevant action that SUCCEEDED, keyed by request ID like
// LogError. It exists so audit lines don't have to borrow LogError, which logs at error
// level and would file every successful action as a fault — polluting error alerting and
// burying real failures.
func LogAudit(r *http.Request, msg string, args ...any) {
	base := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"request_id", middleware.GetReqID(r.Context()),
	}
	slog.Info(msg, append(base, args...)...)
}

// Origin builds an absolute scheme://host[:port] for a sibling surface, reusing
// the port the request arrived on (one binary serves all hosts on one port).
func (e *Env) Origin(r *http.Request, host string) string {
	return originFor(e.Cfg, r, host)
}

// RedirectAfterLogout lands a signed-out visitor on the public root host. Shared
// by every host's POST /logout so sign-out ends in the same place regardless of
// which surface it was triggered from.
func (e *Env) RedirectAfterLogout(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, e.Origin(r, e.Cfg.RootHost)+"/", http.StatusSeeOther) //nolint:gosec // G710: config-pinned origin
}

func originFor(cfg *config.Config, r *http.Request, host string) string {
	scheme := "http"
	if cfg.Secure {
		scheme = "https"
	}
	port := ""
	if _, p, err := net.SplitHostPort(r.Host); err == nil && p != "" {
		port = ":" + p
	}
	return scheme + "://" + host + port
}

// Renderer composes content pages onto the shared base layout and executes them,
// injecting the cross-host URLs and current-user info every page's chrome needs.
type Renderer struct {
	cfg      *config.Config
	pages    map[string]*template.Template
	userInfo UserInfoFunc
	// defaultLayout is the layout used when a render specifies none. Empty means
	// "base" (the app chrome); the marketing surface sets "rootbase".
	defaultLayout string
}

// SetDefaultLayout sets the layout used for renders that don't specify one. The
// marketing surface calls this with "rootbase" so every root page gets the
// public topbar without each handler passing a Layout key.
func (e *Env) SetDefaultLayout(name string) { e.Renderer.defaultLayout = name }

// NewRenderer parses the shared base layout (base.html + icons.html) and composes
// each of the surface's own *.html pages onto it. Each page redefines the
// content/title/header blocks, so every page gets its own cloned template set.
// templateFuncs are helpers available to every page template. mhz/khz present
// the Hz-canonical radio values in the human-readable units the region presets
// use (MHz for frequency, kHz for bandwidth), formatted without trailing zeros.
var templateFuncs = template.FuncMap{
	"mhz": func(hz int64) string { return strconv.FormatFloat(float64(hz)/1e6, 'f', -1, 64) },
	"khz": func(hz int64) string { return strconv.FormatFloat(float64(hz)/1e3, 'f', -1, 64) },
	// markdown renders user-authored markdown (e.g. an org description) to
	// sanitized HTML. Wrap the output in a `.markdown` container for spacing.
	"markdown": Markdown,
	// markdowntext flattens that same markdown to plain text for compact teasers.
	"markdowntext": MarkdownText,
	// ts renders an instant as a <time> element for consistent, locale-aware
	// display. See TimeElement.
	"ts": TimeElement,
	// asset maps a logical static path ("/static/ui.js") to its content-hashed,
	// immutably-cacheable URL. Use it for every /static/ reference in templates.
	"asset": assets.URL,
}

// tsFallbackLayouts maps a display kind to the Go layout used for the server-side
// fallback text (rendered in UTC, labeled, for no-JS / crawlers). ui.js rewrites
// the element into the viewer's locale and zone at load time.
var tsFallbackLayouts = map[string]string{
	"date":         "Jan 2, 2006",
	"datetime":     "Jan 2, 2006, 15:04 UTC",
	"time":         "15:04 UTC",
	"time-seconds": "15:04:05 UTC",
}

// TimeElement renders an instant as
//
//	<time datetime="2006-01-02T15:04:05Z" data-fmt="datetime">Jan 2, 2006, 15:04 UTC</time>
//
// The machine-readable datetime is always a full RFC3339 UTC timestamp; kind
// (date|datetime|time|time-seconds) selects which parts ui.js shows once it
// localizes the element. A zero time renders nothing (call sites guard nil).
//
// The result is template.HTML because it's server-controlled markup: the only
// interpolated values are a formatted timestamp and a fixed, validated kind — no
// user data — so autoescaping is unnecessary here.
func TimeElement(t time.Time, kind string) template.HTML {
	if t.IsZero() {
		return ""
	}
	layout, ok := tsFallbackLayouts[kind]
	if !ok {
		kind, layout = "datetime", tsFallbackLayouts["datetime"]
	}
	utc := t.UTC()
	var b strings.Builder
	b.WriteString(`<time datetime="`)
	b.WriteString(utc.Format(time.RFC3339))
	b.WriteString(`" data-fmt="`)
	b.WriteString(kind)
	b.WriteString(`">`)
	b.WriteString(utc.Format(layout))
	b.WriteString(`</time>`)
	return template.HTML(b.String()) //nolint:gosec // G203: server-controlled timestamp + fixed enum, no user data
}

func NewRenderer(cfg *config.Config, surfaceTemplates fs.FS) (*Renderer, error) {
	base, err := template.New("").Funcs(templateFuncs).ParseFS(sharedTemplatesFS, "templates/base.html", "templates/icons.html", "templates/org_tabs.html", "templates/repeater_tabs.html", "templates/command_grid.html", "templates/org_access.html")
	if err != nil {
		return nil, err
	}
	all, err := fs.Glob(surfaceTemplates, "templates/*.html")
	if err != nil {
		return nil, err
	}
	// The shared layout/partials are composed in above; skip them if they happen
	// to live in the surface's template dir.
	shared := map[string]bool{"templates/base.html": true, "templates/icons.html": true}
	pages := map[string]*template.Template{}
	// Compose the cross-surface pages first, then the surface's own pages (a
	// surface page of the same name would override, but none should collide).
	for _, p := range sharedPages {
		clone, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := clone.ParseFS(sharedTemplatesFS, p); err != nil {
			return nil, err
		}
		pages[strings.TrimPrefix(p, "templates/")] = clone
	}
	for _, p := range all {
		if shared[p] {
			continue
		}
		clone, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := clone.ParseFS(surfaceTemplates, p); err != nil {
			return nil, err
		}
		pages[strings.TrimPrefix(p, "templates/")] = clone
	}
	return &Renderer{cfg: cfg, pages: pages}, nil
}

// Pages returns the composed page templates keyed by file name. Intended for
// startup/composition tests.
func (rn *Renderer) Pages() map[string]*template.Template { return rn.pages }

// Render executes a content page within its layout (default "base"; pages can
// opt into another via the "Layout" data key), with a 200 status.
func (rn *Renderer) Render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	rn.render(w, r, http.StatusOK, page, data)
}

// render is Render with an explicit status code (error pages pass 404/500/…).
func (rn *Renderer) render(w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	// Absolute origins for cross-host links: sessions are host-scoped, so a link
	// from one surface to a sibling must be absolute.
	data["AppURL"] = originFor(rn.cfg, r, rn.cfg.PrimaryHost)
	data["AuthURL"] = originFor(rn.cfg, r, rn.cfg.AuthHost)
	data["RootURL"] = originFor(rn.cfg, r, rn.cfg.RootHost)
	// LogoutURL: sign-out is a POST that revokes the login row, so it must target a
	// host that owns a /logout endpoint AND holds this browser's session. The app
	// host, auth host, and custom org domains all do (relative "/logout", a
	// same-host POST). The root host is strictly side-effect-free GET (see
	// docs/auth-cross-host.md), so it has no logout of its own — the template hides
	// the control there and the user signs out from the app dashboard instead.
	if HostWithoutPort(r.Host) != rn.cfg.RootHost {
		data["LogoutURL"] = "/logout"
	}
	// UserTZ is the viewer's saved IANA zone (empty = auto-detect); the base
	// layout exposes it as <html data-tz> so ui.js localizes <time> elements.
	data["UserTZ"] = ""
	if rn.userInfo != nil {
		if name, canAdmin, tz, ok := rn.userInfo(r.Context()); ok {
			data["UserName"] = name
			data["CanAdmin"] = canAdmin
			data["UserTZ"] = tz
		}
	}
	// Per-request CSP nonce for inline <script nonce="{{.Nonce}}"> blocks.
	data["Nonce"] = NonceFromContext(r.Context())
	t, ok := rn.pages[page]
	if !ok {
		slog.Error("render: unknown page",
			"page", page, "request_id", middleware.GetReqID(r.Context()))
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	layout, _ := data["Layout"].(string)
	if layout == "" {
		layout = rn.defaultLayout
	}
	if layout == "" {
		layout = "base"
	}
	// Render into a buffer first: executing straight to w commits a 200 and a
	// partial body the moment the template writes anything, so a mid-render
	// failure would both leak the error text (template/query internals) and
	// corrupt the page. Buffering lets us fail cleanly with a generic 500 and
	// log the real cause server-side, keyed by request ID.
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, layout, data); err != nil {
		slog.Error("template render failed",
			"page", page, "layout", layout,
			"request_id", middleware.GetReqID(r.Context()), "err", err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// --- shared HTTP helpers (used across surfaces) ---

// CommonMiddleware applies the generic middleware every surface needs. Session
// loading is added by each surface (it owns the auth service), so web stays
// auth-free. chi requires all Use() calls before any route is registered.
func (e *Env) CommonMiddleware(r chi.Router) {
	r.Use(middleware.RequestID)
	r.Use(CaptureRemoteAddr)    // preserve the true TCP peer before we resolve
	r.Use(e.resolveClientIP)    // trusted-proxy-aware X-Forwarded-For resolution
	r.Use(e.securityHeaders)    // CSP (+ per-request script nonce) and hardening headers
	r.Use(blockCrossSiteWrites) // CSRF second layer, before any handler reads the body
	r.Use(limitBody)            // cap request bodies before any handler reads them
	r.Use(compressHTML)         // gzip the server-rendered pages
	r.Use(middleware.Recoverer)
}

// compressibleTypes is deliberately just text/html — the server-rendered pages,
// which is where the entire win is (a page is 15–30 KB of markup that shrinks
// ~80%). Everything else is left alone on purpose:
//
//   - Static CSS/JS/SVG are already pre-compressed once at startup with brotli and
//     gzip at their best levels (see assets.go), and served with their own
//     Content-Encoding — which chi's compressor detects and skips. Listing those
//     types here would only add a redundant on-the-fly path for the rare identity
//     fallback.
//   - application/json is skipped because ServeJSONCached computes a *strong* ETag
//     over the uncompressed bytes; a strong validator is supposed to be unique per
//     representation, and encodings are separate representations. The one JSON
//     endpoint is a small list of map points, so there's little to gain. If it ever
//     grows, compress it at the handler and weaken the ETag there.
//
// Note on BREACH: compressing a response that mixes a secret with
// attacker-influenced text can leak the secret through response sizes. No secret is
// rendered into an HTML body today — the auth handoff code travels in a redirect's
// Location header, not the page. If a CSRF token is ever embedded in forms (audit
// S1), revisit this: the usual mitigation is per-response length masking.
var compressibleTypes = []string{"text/html"}

// compressHTML gzips server-rendered HTML. Placed after the header middleware so
// those headers are set before the body is written, and outside Recoverer so a
// recovered panic's plain-text 500 passes through uncompressed. Responses that
// already carry a Content-Encoding (the pre-compressed static assets) are skipped
// by the compressor, and it adds Vary: Accept-Encoding so the cacheable public
// pages can't be served a gzip body to a client that didn't ask for one.
//
// Level 5 rather than best: on a 30 KB page the last few levels buy a couple of
// percent for meaningfully more CPU per request. Brotli isn't offered here — chi
// ships gzip/deflate, and on-the-fly brotli at a competitive quality costs more
// than it returns for bodies this size. The static assets, compressed once at
// startup, do get brotli.
var compressHTML = middleware.Compress(5, compressibleTypes...)

// maxRequestBody caps the request body every surface will read. The app has no
// file uploads — the largest bodies are form posts and small JSON (serial-setup
// command lists, markdown docs) — so 1 MiB is generous while stopping a client
// from streaming an arbitrarily large body into memory before per-field limits
// can apply. WebSocket upgrades carry no request body, so this doesn't affect
// the console/confirm sockets.
const maxRequestBody = 1 << 20 // 1 MiB

// limitBody caps r.Body so an oversized request fails fast — a read past the
// limit errors and the handler surfaces its generic 400/500 — instead of
// buffering unbounded data.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		next.ServeHTTP(w, r)
	})
}

// SharedRoutes registers endpoints every surface needs (health, static assets).
func (e *Env) SharedRoutes(r chi.Router) {
	r.Get("/healthz", e.healthz)
	r.Handle("/static/*", http.StripPrefix("/static/", http.HandlerFunc(assets.serveHTTP)))
}

// healthz is a readiness probe: it pings the database (briefly) so a broken pool
// or unreachable DB fails the check — letting a load balancer route away or an
// orchestrator restart the instance — rather than reporting healthy while every
// request 500s.
func (e *Env) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := e.Store.Pool().Ping(ctx); err != nil {
		LogError(r, "healthz: db ping failed", err)
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

// HostWithoutPort strips a trailing :port from a request Host, if present.
func HostWithoutPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// RedirectFlash 303-redirects to path with a single flash query param (key=msg,
// escaped), picking ? or & as the separator.
func RedirectFlash(w http.ResponseWriter, r *http.Request, path, key, msg string) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	http.Redirect(w, r, path+sep+key+"="+url.QueryEscape(msg), http.StatusSeeOther)
}

// RedirectErr is RedirectFlash with the conventional "error" key.
func RedirectErr(w http.ResponseWriter, r *http.Request, path, msg string) {
	RedirectFlash(w, r, path, "error", msg)
}

// Dispatcher routes by hostname across the three surfaces: the auth host, the
// root (public discovery) host, and — for everything else, including custom org
// domains — the app host. AuthHost and RootHost are always configured (see
// config.Load); the WWWHost redirect is optional.
func Dispatcher(cfg *config.Config, authH, rootH, appH http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := HostWithoutPort(r.Host)
		switch {
		case strings.EqualFold(host, cfg.AuthHost):
			authH.ServeHTTP(w, r)
		case strings.EqualFold(host, cfg.RootHost):
			rootH.ServeHTTP(w, r)
		case cfg.WWWHost != "" && strings.EqualFold(host, cfg.WWWHost):
			http.Redirect(w, r, originFor(cfg, r, cfg.RootHost)+r.URL.RequestURI(), http.StatusMovedPermanently) //nolint:gosec // G710: local path or config-pinned origin
		default:
			appH.ServeHTTP(w, r)
		}
	})
}
