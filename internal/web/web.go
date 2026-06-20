// Package web assembles the HTTP server: routing, templates, and static
// assets, wiring together auth, identity, and the data store.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"

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
	store     *store.Store
	auth      *auth.Service
	identity  *identity.Service
	cfg       *config.Config
	templates *template.Template
	router    chi.Router
}

// NewServer constructs the HTTP server and its routes.
func NewServer(st *store.Store, authSvc *auth.Service, idSvc *identity.Service, cfg *config.Config) (*Server, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{store: st, auth: authSvc, identity: idSvc, cfg: cfg, templates: tmpl}
	s.routes()
	return s, nil
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

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	staticSub, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Public auth pages + JSON ceremony endpoints.
	r.Get("/login", s.pageLogin)
	r.Get("/signup", s.pageSignup)
	r.Post("/login/password", s.auth.LoginPassword)
	r.Post("/signup/password", s.auth.SignupPassword)
	r.Post("/api/register/begin", s.auth.RegisterBegin)
	r.Post("/api/register/finish", s.auth.RegisterFinish)
	r.Post("/api/login/begin", s.auth.LoginBegin)
	r.Post("/api/login/finish", s.auth.LoginFinish)
	r.Get("/invite/{token}", s.pageInvite)        // public: handles logged-out state
	r.Get("/org-invite/{token}", s.pageOrgInvite) // public: handles logged-out state

	// Authenticated area.
	r.Group(func(r chi.Router) {
		r.Use(s.auth.RequireUser)
		r.Get("/", s.pageDashboard)
		r.Post("/logout", s.handleLogout)
		r.Get("/repeaters/add", s.pageAddRepeater)
		r.Post("/repeaters", s.handleAddRepeater)
		r.Get("/repeaters/{id}/edit", s.pageEditRepeater)
		r.Post("/repeaters/{id}/edit", s.handleEditRepeater)
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
		r.Get("/repeaters/{id}/orgs", s.pageRepeaterOrgs)
		r.Get("/repeaters/{id}/orgs/{orgID}/contribute", s.pageContribute)
		r.Post("/repeaters/{id}/orgs/{orgID}/contribute", s.handleContribute)
		r.Post("/repeaters/{id}/orgs/{orgID}/withdraw", s.handleWithdraw)
		r.Post("/invite/{token}/accept", s.handleAcceptInvite)

		r.Get("/orgs", s.pageOrgs)
		r.Post("/orgs", s.handleCreateOrg)
		r.Get("/orgs/{id}", s.pageOrg)
		r.Post("/orgs/{id}/leave", s.handleLeaveOrg)
		r.Post("/orgs/{id}/invite", s.handleCreateOrgInvite)
		r.Post("/orgs/{id}/invite/delete", s.handleDeleteOrgInvite)
		r.Post("/orgs/{id}/members/{userID}", s.handleSetOrgMember)
		r.Get("/orgs/{id}/permissions", s.pageOrgPermissions)
		r.Post("/orgs/{id}/permissions", s.handleSaveOrgPermissions)
		r.Post("/org-invite/{token}/accept", s.handleAcceptOrgInvite)

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
	// Clone so we can associate the page's blocks without mutating the shared set.
	t, err := s.templates.Clone()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := t.ParseFS(templatesFS, "templates/"+page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base", data); err != nil {
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
		"Error": r.URL.Query().Get("error"),
		"Next":  r.URL.Query().Get("next"),
	})
}

func (s *Server) pageSignup(w http.ResponseWriter, r *http.Request) {
	if s.auth.CurrentUserID(r.Context()) != 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.auth.SetNext(r.Context(), r.URL.Query().Get("next"))
	s.render(w, r, "signup.html", map[string]any{
		"Error": r.URL.Query().Get("error"),
		"Next":  r.URL.Query().Get("next"),
	})
}

func (s *Server) pageDashboard(w http.ResponseWriter, r *http.Request) {
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
	// Split owned vs directly-shared for the two dashboard sections.
	var owned, shared []*store.Repeater
	for _, rp := range repeaters {
		if rp.Shared {
			shared = append(shared, rp)
		} else {
			owned = append(owned, rp)
		}
	}
	s.render(w, r, "dashboard.html", map[string]any{
		"Owned":     owned,
		"Shared":    shared,
		"Reconsent": reconsent,
		"Error":     r.URL.Query().Get("error"),
	})
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
