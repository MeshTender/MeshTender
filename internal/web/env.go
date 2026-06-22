// Package web is the shared HTTP foundation: template rendering, common
// middleware/helpers, and the host dispatcher that the marketing/auth/core
// surface packages build on. It deliberately does NOT import the surface
// packages (or internal/auth), so those can import web without a cycle.
package web

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/identity"
	"github.com/jleight/meshtender/internal/store"
)

//go:embed templates/base.html templates/icons.html templates/org_tabs.html templates/org_public.html templates/org_config.html templates/org_permissions.html templates/org_repeaters.html
var sharedTemplatesFS embed.FS

// sharedPages are full content pages (not just layout partials) that more than
// one surface renders. They're composed onto the base layout for every surface,
// so the root host (anonymous) and the app host (signed-in) can render the same
// public org page without duplicating the template.
var sharedPages = []string{"templates/org_public.html", "templates/org_config.html", "templates/org_permissions.html", "templates/org_repeaters.html"}

//go:embed static/*
var staticFS embed.FS

// UserInfoFunc reports the signed-in user's display name and admin flag for the
// page chrome. ok is false when no user is signed in. Injected by the assembler
// (which wires it from the auth service + store) so web stays auth-free.
type UserInfoFunc func(ctx context.Context) (name string, canAdmin bool, ok bool)

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

// Origin builds an absolute scheme://host[:port] for a sibling surface, reusing
// the port the request arrived on (one binary serves all hosts on one port).
func (e *Env) Origin(r *http.Request, host string) string {
	return originFor(e.Cfg, r, host)
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
}

// NewRenderer parses the shared base layout (base.html + icons.html) and composes
// each of the surface's own *.html pages onto it. Each page redefines the
// content/title/header blocks, so every page gets its own cloned template set.
func NewRenderer(cfg *config.Config, surfaceTemplates fs.FS) (*Renderer, error) {
	base, err := template.New("").ParseFS(sharedTemplatesFS, "templates/base.html", "templates/icons.html", "templates/org_tabs.html")
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
// opt into another via the "Layout" data key).
func (rn *Renderer) Render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	// Absolute origins for cross-host links (sessions are host-scoped, so a link
	// from root/app to a sibling surface must be absolute). Empty in single-host
	// mode, where templates' relative paths already resolve correctly.
	if rn.cfg.AuthHost != "" {
		data["AppURL"] = originFor(rn.cfg, r, rn.cfg.PrimaryHost)
		data["AuthURL"] = originFor(rn.cfg, r, rn.cfg.AuthHost)
		if rn.cfg.RootHost != "" {
			data["RootURL"] = originFor(rn.cfg, r, rn.cfg.RootHost)
		}
	}
	if rn.userInfo != nil {
		if name, canAdmin, ok := rn.userInfo(r.Context()); ok {
			data["UserName"] = name
			data["CanAdmin"] = canAdmin
		}
	}
	t, ok := rn.pages[page]
	if !ok {
		http.Error(w, "unknown page: "+page, http.StatusInternalServerError)
		return
	}
	layout, _ := data["Layout"].(string)
	if layout == "" {
		layout = "base"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, layout, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// --- shared HTTP helpers (used across surfaces) ---

// CommonMiddleware applies the generic middleware every surface needs. Session
// loading is added by each surface (it owns the auth service), so web stays
// auth-free. chi requires all Use() calls before any route is registered.
func (e *Env) CommonMiddleware(r chi.Router) {
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
}

// SharedRoutes registers endpoints every surface needs (health, static assets).
func (e *Env) SharedRoutes(r chi.Router) {
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	staticSub, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
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

// Dispatcher routes by hostname across the surfaces. authH/rootH/appH are the
// per-surface handlers; rootH may be nil (single-host). When AuthHost is empty,
// appH serves everything (single-host mode).
func Dispatcher(cfg *config.Config, authH, rootH, appH http.Handler) http.Handler {
	if cfg.AuthHost == "" {
		return appH
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := HostWithoutPort(r.Host)
		switch {
		case strings.EqualFold(host, cfg.AuthHost):
			authH.ServeHTTP(w, r)
		case rootH != nil && strings.EqualFold(host, cfg.RootHost):
			rootH.ServeHTTP(w, r)
		case cfg.WWWHost != "" && strings.EqualFold(host, cfg.WWWHost):
			http.Redirect(w, r, originFor(cfg, r, cfg.RootHost)+r.URL.RequestURI(), http.StatusMovedPermanently)
		default:
			appH.ServeHTTP(w, r)
		}
	})
}
