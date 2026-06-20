// Package web assembles the HTTP server: routing, templates, and static
// assets, wiring together auth, identity, and the data store.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/identity"
	"github.com/jleight/meshtender/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Server holds the dependencies shared by HTTP handlers.
type Server struct {
	store    *store.Store
	auth     *auth.Service
	identity *identity.Service
	cfg      *config.Config
	// pages holds each content page pre-composed with the shared layouts and
	// partials, keyed by file name (e.g. "dashboard.html"). Built once at
	// startup since the templates are embedded and never change at runtime.
	pages  map[string]*template.Template
	router chi.Router
	// lookupTXT resolves DNS TXT records; injectable so domain verification is
	// testable. Defaults to net.LookupTXT.
	lookupTXT func(name string) ([]string, error)
}

// NewServer constructs the HTTP server and its routes.
func NewServer(st *store.Store, authSvc *auth.Service, idSvc *identity.Service, cfg *config.Config) (*Server, error) {
	pages, err := buildPages()
	if err != nil {
		return nil, err
	}
	s := &Server{store: st, auth: authSvc, identity: idSvc, cfg: cfg, pages: pages, lookupTXT: net.LookupTXT}
	s.routes()
	return s, nil
}

// sharedTemplates are the layouts and partials shared by every page (the root
// layouts live in base.html; reusable snippets like the icon set in icons.html).
// They define no "content"/"title" blocks of their own, so they can be the
// common base each page is composed onto.
var sharedTemplates = []string{"templates/base.html", "templates/icons.html"}

// buildPages composes each content page with the shared layouts/partials once,
// returning a map keyed by the page's file name. Each page redefines the
// "content"/"title"/"header" blocks, so every page needs its own template set
// rather than one shared set (where the blocks would collide).
func buildPages() (map[string]*template.Template, error) {
	base, err := template.New("").ParseFS(templatesFS, sharedTemplates...)
	if err != nil {
		return nil, err
	}
	all, err := fs.Glob(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	shared := map[string]bool{}
	for _, p := range sharedTemplates {
		shared[p] = true
	}
	pages := map[string]*template.Template{}
	for _, p := range all {
		if shared[p] {
			continue
		}
		clone, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := clone.ParseFS(templatesFS, p); err != nil {
			return nil, err
		}
		pages[strings.TrimPrefix(p, "templates/")] = clone
	}
	return pages, nil
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) routes() {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	// scs must wrap everything that touches the session.
	r.Use(s.auth.Sessions.LoadAndSave)
	// Custom org domains: a verified CNAMEd host serves that org's public page.
	r.Use(s.customDomain)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	staticSub, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Root: marketing landing for visitors, overview dashboard for members.
	r.Get("/", s.pageHome)

	// Public auth pages + JSON ceremony endpoints.
	r.Get("/login", s.pageLogin)
	r.Get("/signup", s.pageSignup)
	// Throttle credential submission per client IP to blunt password guessing
	// and signup spam. Allows a burst (e.g. fat-fingered retries), then ~1 try
	// every 6s; bcrypt's cost is the second line of defense.
	authLimit := newRateLimiter(10, 6*time.Second)
	r.With(authLimit.middleware).Post("/login/password", s.auth.LoginPassword)
	r.With(authLimit.middleware).Post("/signup/password", s.auth.SignupPassword)
	r.Post("/api/register/begin", s.auth.RegisterBegin)
	r.Post("/api/register/finish", s.auth.RegisterFinish)
	r.Post("/api/login/begin", s.auth.LoginBegin)
	r.Post("/api/login/finish", s.auth.LoginFinish)
	r.Post("/api/login/discoverable/begin", s.auth.LoginDiscoverableBegin)
	r.Post("/api/login/discoverable/finish", s.auth.LoginDiscoverableFinish)
	r.Get("/invite/{token}", s.pageInvite) // public: handles logged-out state
	r.Get("/orgs", s.pageOrgs)             // public: organization directory
	r.Get("/orgs/{id}", s.pageOrg)         // public: org page (public view for non-members)

	// Authenticated area.
	r.Group(func(r chi.Router) {
		r.Use(s.auth.RequireUser)
		r.Get("/repeaters", s.pageRepeaters)
		r.Post("/logout", s.handleLogout)
		r.Get("/account", s.pageAccount)
		r.Post("/account/profile", s.handleUpdateProfile)
		r.Post("/account/password", s.handleChangePassword)
		r.Post("/account/passkeys/delete", s.handleDeletePasskey)
		r.Get("/repeaters/add", s.pageAddRepeater)
		r.Post("/repeaters", s.handleAddRepeater)
		r.Get("/repeaters/{id}", s.pageRepeater)
		r.Get("/repeaters/{id}/added", s.pageRepeaterAdded)
		r.Get("/repeaters/{id}/edit", s.pageEditRepeater)
		r.Post("/repeaters/{id}/edit", s.handleEditRepeater)
		r.Get("/repeaters/{id}/delete", s.pageDeleteRepeater)
		r.Post("/repeaters/{id}/delete", s.handleDeleteRepeater)
		r.Get("/repeaters/{id}/confirm", s.pageConfirm)
		r.Get("/repeaters/{id}/ws", s.wsConfirm)
		r.Get("/repeaters/{id}/console", s.pageConsole)
		r.Get("/repeaters/{id}/console/ws", s.wsConsole)
		r.Get("/repeaters/{id}/log", s.pageCommandLog)
		r.Get("/repeaters/{id}/share", s.pageShare)
		r.Post("/repeaters/{id}/share/link", s.handleCreateLink)
		r.Post("/repeaters/{id}/share/link/delete", s.handleDeleteInvite)
		r.Post("/repeaters/{id}/unshare", s.handleUnshare)
		r.Get("/repeaters/{id}/share/{userID}/commands", s.pageShareCommands)
		r.Post("/repeaters/{id}/share/{userID}/commands", s.handleSetShareCommands)
		r.Get("/repeaters/{id}/orgs/{orgID}/contribute", s.pageContribute)
		r.Post("/repeaters/{id}/orgs/{orgID}/contribute", s.handleContribute)
		r.Post("/repeaters/{id}/orgs/{orgID}/withdraw", s.handleWithdraw)
		r.Post("/invite/{token}/accept", s.handleAcceptInvite)

		r.Get("/orgs/new", s.pageNewOrg)
		r.Post("/orgs", s.handleCreateOrg)
		r.Post("/orgs/{id}/edit", s.handleEditOrg)
		r.Post("/orgs/{id}/join", s.handleJoinOrg)
		r.Post("/orgs/{id}/leave", s.handleLeaveOrg)
		r.Post("/orgs/{id}/members/{userID}", s.handleSetOrgMember)
		r.Get("/orgs/{id}/permissions", s.pageOrgPermissions)
		r.Post("/orgs/{id}/permissions", s.handleSaveOrgPermissions)
		r.Post("/orgs/{id}/domains", s.handleAddOrgDomain)
		r.Post("/orgs/{id}/domains/verify", s.handleVerifyOrgDomain)
		r.Post("/orgs/{id}/domains/delete", s.handleDeleteOrgDomain)

		r.Route("/admin", func(r chi.Router) {
			r.With(s.requireCap(capAny)).Get("/", s.pageAdmin)
			r.With(s.requireCap(capCatalog)).Get("/catalog", s.pageCatalog)
			r.With(s.requireCap(capCatalog)).Post("/catalog/{id}", s.handleUpdateCommand)
			r.With(s.requireCap(capUsers)).Get("/users", s.pageUsers)
			r.With(s.requireCap(capUsers)).Post("/users/{id}", s.handleSetUserCaps)
		})
	})

	s.router = r
}

// customDomain serves an org's public page when the request arrives on one of
// the org's verified custom domains. Any non-root path is redirected to the
// canonical host, where auth and management (and the WebAuthn RP) live.
func (s *Server) customDomain(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := hostWithoutPort(r.Host)
		if host == "" || host == "localhost" || strings.EqualFold(host, s.cfg.PrimaryHost) {
			next.ServeHTTP(w, r)
			return
		}
		org, ok, err := s.store.OrgByVerifiedDomain(r.Context(), strings.ToLower(host))
		if err != nil || !ok {
			next.ServeHTTP(w, r) // unknown host: serve the app normally
			return
		}
		if r.URL.Path == "/" {
			s.renderOrgPublic(w, r, org, false)
			return
		}
		http.Redirect(w, r, "https://"+s.cfg.PrimaryHost+r.URL.RequestURI(), http.StatusFound)
	})
}

// hostWithoutPort strips a trailing :port from a request Host, if present.
func hostWithoutPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// render executes a content template within the base layout.
func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	// Attach the current user's display name for the layout, when authenticated.
	if uid := s.auth.CurrentUserID(r.Context()); uid != 0 {
		if u, err := s.store.GetUserByID(r.Context(), uid); err == nil {
			data["UserName"] = u.Name()
			data["CanAdmin"] = u.CapManageUsers || u.CapManageCatalog
		}
	}
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "unknown page: "+page, http.StatusInternalServerError)
		return
	}
	// Pages may opt into an alternate root layout (e.g. the centered "authbase"
	// for sign-in/up) via the "Layout" data key; everything else uses "base".
	layout, _ := data["Layout"].(string)
	if layout == "" {
		layout = "base"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, layout, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) pageLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth.CurrentUserID(r.Context()) != 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.auth.SetNext(r.Context(), r.URL.Query().Get("next"))
	s.render(w, r, "login.html", map[string]any{
		"Layout": "authbase",
		"Error":  r.URL.Query().Get("error"),
		"Next":   r.URL.Query().Get("next"),
	})
}

func (s *Server) pageSignup(w http.ResponseWriter, r *http.Request) {
	if s.auth.CurrentUserID(r.Context()) != 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.auth.SetNext(r.Context(), r.URL.Query().Get("next"))
	s.render(w, r, "signup.html", map[string]any{
		"Layout": "authbase",
		"Error":  r.URL.Query().Get("error"),
		"Next":   r.URL.Query().Get("next"),
	})
}

// pageHome serves the marketing landing page to visitors and the overview
// dashboard to signed-in members.
func (s *Server) pageHome(w http.ResponseWriter, r *http.Request) {
	if s.auth.CurrentUserID(r.Context()) == 0 {
		s.render(w, r, "landing.html", map[string]any{"Layout": "landingbase"})
		return
	}
	s.pageDashboard(w, r)
}

// pageRepeaters lists every repeater the user owns or has been shared, split
// into owned and shared sections.
func (s *Server) pageRepeaters(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	repeaters, err := s.store.ListRepeatersForUser(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load repeaters", http.StatusInternalServerError)
		return
	}
	reconsent, err := s.store.OwnedRepeatersNeedingReconsent(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load org state", http.StatusInternalServerError)
		return
	}
	shareCounts, err := s.store.RepeaterSharingCounts(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load sharing", http.StatusInternalServerError)
		return
	}
	owned, shared := splitOwnedShared(repeaters)
	s.render(w, r, "repeaters.html", map[string]any{
		"Owned":       owned,
		"Shared":      shared,
		"Reconsent":   reconsent,
		"ShareCounts": shareCounts,
		"Error":       r.URL.Query().Get("error"),
	})
}

// pageDashboard renders the signed-in overview: summary stats, short lists of
// repeaters and organizations, a map of owned repeaters, and recent activity.
func (s *Server) pageDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.auth.CurrentUserID(ctx)

	repeaters, err := s.store.ListRepeatersForUser(ctx, uid)
	if err != nil {
		http.Error(w, "could not load repeaters", http.StatusInternalServerError)
		return
	}
	owned, shared := splitOwnedShared(repeaters)

	reconsent, err := s.store.OwnedRepeatersNeedingReconsent(ctx, uid)
	if err != nil {
		http.Error(w, "could not load org state", http.StatusInternalServerError)
		return
	}
	orgs, err := s.store.ListOrgsForUser(ctx, uid)
	if err != nil {
		http.Error(w, "could not load organizations", http.StatusInternalServerError)
		return
	}
	recent, err := s.store.ListRecentCommandsForOwner(ctx, uid, 8)
	if err != nil {
		http.Error(w, "could not load activity", http.StatusInternalServerError)
		return
	}
	shareCounts, err := s.store.RepeaterSharingCounts(ctx, uid)
	if err != nil {
		http.Error(w, "could not load sharing", http.StatusInternalServerError)
		return
	}

	// Summary counts and the owned repeaters that have a stored location.
	confirmed, unconfirmed := 0, 0
	var mapped []*store.Repeater
	for _, rp := range owned {
		if rp.Confirmed {
			confirmed++
		} else {
			unconfirmed++
		}
		if rp.Latitude != nil && rp.Longitude != nil {
			mapped = append(mapped, rp)
		}
	}

	s.render(w, r, "dashboard.html", map[string]any{
		"OwnedCount":  len(owned),
		"SharedCount": len(shared),
		"OrgCount":    len(orgs),
		"Confirmed":   confirmed,
		"Unconfirmed": unconfirmed,
		"Owned":       firstRepeaters(owned, 5),
		"Shared":      firstRepeaters(shared, 5),
		"Orgs":        firstOrgs(orgs, 5),
		"Mapped":      mapped,
		"Recent":      recent,
		"Reconsent":   reconsent,
		"ShareCounts": shareCounts,
		"Error":       r.URL.Query().Get("error"),
	})
}

// splitOwnedShared partitions a repeater list into owned and directly-shared.
func splitOwnedShared(repeaters []*store.Repeater) (owned, shared []*store.Repeater) {
	for _, rp := range repeaters {
		if rp.Shared {
			shared = append(shared, rp)
		} else {
			owned = append(owned, rp)
		}
	}
	return owned, shared
}

func firstRepeaters(rs []*store.Repeater, n int) []*store.Repeater {
	if len(rs) > n {
		return rs[:n]
	}
	return rs
}

func firstOrgs(os []store.OrgMembership, n int) []store.OrgMembership {
	if len(os) > n {
		return os[:n]
	}
	return os
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	next := r.FormValue("next")
	_ = s.auth.Logout(r.Context())
	dest := "/login"
	if auth.SafeLocalPath(next) {
		dest = "/login?next=" + url.QueryEscape(next)
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
