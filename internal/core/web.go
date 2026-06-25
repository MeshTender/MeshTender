// Package core is the application (app host) surface — the authenticated product
// — and the composition root that assembles the auth, marketing, and core
// surfaces behind the host dispatcher.
package core

import (
	"context"
	"embed"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/identity"
	"github.com/jleight/meshtender/internal/marketing"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Handlers carries the shared environment plus the auth service for the app
// surface's HTTP handlers.
type Handlers struct {
	*web.Env
	Auth *auth.Service
}

// Server is the built HTTP entry point.
type Server struct{ handler http.Handler }

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// NewServer assembles every surface and the host dispatcher. The deployment is
// always split-host (auth / root / app); the auth host serves sign-in + account,
// the root host serves marketing + public org discovery, and the app host (plus
// custom org domains and any unrecognized host) serves the product.
func NewServer(st *store.Store, authSvc *auth.Service, idSvc *identity.Service, cfg *config.Config) (*Server, error) {
	deps := web.Deps{
		Store:    st,
		Identity: idSvc,
		Cfg:      cfg,
		UserInfo: func(ctx context.Context) (string, bool, bool) {
			uid := authSvc.CurrentUserID(ctx)
			if uid == 0 {
				return "", false, false
			}
			u, err := st.GetUserByID(ctx, uid)
			if err != nil {
				return "", false, false
			}
			return u.Name(), u.CapManageUsers || u.CapManageCatalog, true
		},
		LookupTXT: net.LookupTXT,
	}

	coreEnv, err := web.NewEnv(deps, templatesFS)
	if err != nil {
		return nil, err
	}
	app := &Handlers{Env: coreEnv, Auth: authSvc}

	authH, err := auth.NewWeb(deps, authSvc)
	if err != nil {
		return nil, err
	}
	mkH, err := marketing.New(deps, authSvc)
	if err != nil {
		return nil, err
	}

	// Custom org domains and unrecognized hosts fall through to the app router,
	// wrapped by marketing's custom-domain interceptor (it serves verified hosts'
	// public org pages).
	appHandler := mkH.CustomDomain(app.appRouter())
	handler := web.Dispatcher(cfg, authH.Routes(), mkH.Routes(), appHandler)
	return &Server{handler: handler}, nil
}

// baseMW applies the shared middleware plus session loading.
func (s *Handlers) baseMW(r chi.Router) {
	s.CommonMiddleware(r)
	r.Use(s.Auth.Sessions.LoadAndSave)
	r.Use(s.Auth.ValidateSession)
}

// redirectToAuthLogin / redirectToAuthSignup are the app-host entry points that
// initiate the auth handoff (setting the state cookie) and bounce to the auth
// host. Cross-host "sign in"/"create account" CTAs target these, never the auth
// host directly, so the state cookie is always established first.
func (s *Handlers) redirectToAuthLogin(w http.ResponseWriter, r *http.Request) {
	s.Auth.StartLogin(w, r, r.URL.Query().Get("next"))
}

func (s *Handlers) redirectToAuthSignup(w http.ResponseWriter, r *http.Request) {
	s.Auth.StartSignup(w, r, r.URL.Query().Get("next"))
}

// appRouter is the product surface served on the app host.
func (s *Handlers) appRouter() chi.Router {
	r := chi.NewRouter()
	s.baseMW(r)
	s.SharedRoutes(r)

	// The handoff endpoint that turns an auth-host sign-in into an app session.
	r.Get("/session/callback", s.Auth.SessionCallback)
	// Credential UI lives on the auth host; these initiate the handoff there.
	r.Get("/login", s.redirectToAuthLogin)
	r.Get("/signup", s.redirectToAuthSignup)

	r.Get("/", s.pageHome)
	r.Get("/invite/{token}", s.pageInvite) // public: handles logged-out state

	// Authenticated area.
	r.Group(func(r chi.Router) {
		r.Use(s.Auth.RequireUser)
		r.Get("/orgs", s.pageMyOrgs)   // your organizations; discovery is on root
		r.Get("/orgs/{id}", s.pageOrg) // member view (non-members redirect to root)
		r.Get("/orgs/{id}/members", s.pageOrgMembers)
		r.Get("/orgs/{id}/repeaters", s.pageOrgRepeaters)
		r.Get("/repeaters", s.pageRepeaters)
		r.Post("/logout", s.handleLogout)
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
		r.Get("/repeaters/{id}/docs", s.pageRepeaterDocs)
		r.Post("/repeaters/{id}/docs", s.handleRepeaterDocs)
		r.Get("/repeaters/{id}/maintenance", s.pageRepeaterMaintenance)
		r.Post("/repeaters/{id}/maintenance", s.handleAddMaintenance)
		r.Post("/repeaters/{id}/maintenance/delete", s.handleDeleteMaintenance)
		r.Get("/repeaters/{id}/share", s.pageShare)
		r.Post("/repeaters/{id}/share/link", s.handleCreateLink)
		r.Post("/repeaters/{id}/share/link/delete", s.handleDeleteInvite)
		r.Post("/repeaters/{id}/unshare", s.handleUnshare)
		r.Get("/repeaters/{id}/share/{userID}/commands", s.pageShareCommands)
		r.Post("/repeaters/{id}/share/{userID}/commands", s.handleSetShareCommands)
		r.Post("/repeaters/{id}/share/{userID}/steward", s.handleSetShareSteward)
		r.Post("/repeaters/{id}/orgs/{orgID}/participation", s.handleSetRepeaterOrg)
		r.Post("/invite/{token}/accept", s.handleAcceptInvite)

		r.Get("/orgs/new", s.pageNewOrg)
		r.Post("/orgs", s.handleCreateOrg)
		r.Post("/orgs/{id}/edit", s.handleEditOrg)
		r.Get("/orgs/{id}/join", s.pageJoinOrg)
		r.Post("/orgs/{id}/join", s.handleJoinOrg)
		r.Post("/orgs/{id}/leave", s.handleLeaveOrg)
		r.Post("/orgs/{id}/members/{userID}", s.handleSetOrgMember)
		r.Get("/orgs/{id}/my-commands", s.pageOrgCommands)
		r.Post("/orgs/{id}/my-commands", s.handleSaveOrgCommands)
		r.Get("/orgs/{id}/config", s.pageOrgConfig)
		r.Get("/orgs/{id}/config/edit", s.pageOrgConfigEdit)
		r.Post("/orgs/{id}/config/edit", s.handleSaveOrgConfig)
		// Custom org domains are hidden for now — the hosting/TLS infrastructure
		// isn't in place yet. Leave the handlers in tree but don't expose them.
		// r.Post("/orgs/{id}/domains", s.handleAddOrgDomain)
		// r.Post("/orgs/{id}/domains/verify", s.handleVerifyOrgDomain)
		// r.Post("/orgs/{id}/domains/delete", s.handleDeleteOrgDomain)

		r.Route("/admin", func(r chi.Router) {
			r.With(s.requireCap(capAny)).Get("/", s.pageAdmin)
			r.With(s.requireCap(capAny)).Get("/proxy-test", s.pageProxyTest)
			r.With(s.requireCap(capCatalog)).Get("/catalog", s.pageCatalog)
			r.With(s.requireCap(capCatalog)).Post("/catalog/{id}", s.handleUpdateCommand)
			r.With(s.requireCap(capUsers)).Get("/users", s.pageUsers)
			r.With(s.requireCap(capUsers)).Get("/users/{id}/history", s.pageUserHistory)
			r.With(s.requireCap(capUsers)).Post("/users/{id}", s.handleSetUserCaps)
		})
	})

	return r
}

// pageHome serves the signed-in dashboard; anonymous visitors are sent to sign
// in on the auth host.
func (s *Handlers) pageHome(w http.ResponseWriter, r *http.Request) {
	if s.Auth.CurrentUserID(r.Context()) == 0 {
		s.Auth.StartLogin(w, r, "/")
		return
	}
	s.pageDashboard(w, r)
}

// pageRepeaters lists every repeater the user owns or has been shared, split
// into owned and shared sections.
func (s *Handlers) pageRepeaters(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	repeaters, err := s.Store.ListRepeatersForUser(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load repeaters", http.StatusInternalServerError)
		return
	}
	shareCounts, err := s.Store.RepeaterSharingCounts(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load sharing", http.StatusInternalServerError)
		return
	}
	owned, shared := splitOwnedShared(repeaters)
	s.Render(w, r, "repeaters.html", map[string]any{
		"Owned":       owned,
		"Shared":      shared,
		"ShareCounts": shareCounts,
		"Error":       r.URL.Query().Get("error"),
	})
}

// pageDashboard renders the signed-in overview: summary stats, short lists of
// repeaters and organizations, a map of owned repeaters, and recent activity.
func (s *Handlers) pageDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)

	repeaters, err := s.Store.ListRepeatersForUser(ctx, uid)
	if err != nil {
		http.Error(w, "could not load repeaters", http.StatusInternalServerError)
		return
	}
	owned, shared := splitOwnedShared(repeaters)

	orgs, err := s.Store.ListOrgsForUser(ctx, uid)
	if err != nil {
		http.Error(w, "could not load organizations", http.StatusInternalServerError)
		return
	}
	recent, err := s.Store.ListRecentCommandsForOwner(ctx, uid, 8)
	if err != nil {
		http.Error(w, "could not load activity", http.StatusInternalServerError)
		return
	}
	shareCounts, err := s.Store.RepeaterSharingCounts(ctx, uid)
	if err != nil {
		http.Error(w, "could not load sharing", http.StatusInternalServerError)
		return
	}

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

	s.Render(w, r, "dashboard.html", map[string]any{
		"OwnedCount":  len(owned),
		"SharedCount": len(shared),
		"OrgCount":    len(orgs),
		"Confirmed":   confirmed,
		"Unconfirmed": unconfirmed,
		"Owned":       first(owned, 5),
		"Shared":      first(shared, 5),
		"Mapped":      mapped,
		"Recent":      recent,
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

// first returns the first n elements of s, or all of them if there are fewer.
func first[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// handleLogout signs the user out of the app host, then chains to the auth host's
// /logout to clear the SSO session too — otherwise the surviving SSO session
// would silently re-authenticate on the next request.
func (s *Handlers) handleLogout(w http.ResponseWriter, r *http.Request) {
	_ = s.Auth.Logout(r.Context())
	http.Redirect(w, r, s.Origin(r, s.Cfg.AuthHost)+"/logout", http.StatusSeeOther)
}
