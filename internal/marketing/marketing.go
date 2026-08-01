// Package marketing is the public surface served on the root (apex) host: the
// marketing landing page and organization discovery. It carries no session
// (cookies are host-only), so every page renders the anonymous/public view. It
// also serves verified custom org domains.
package marketing

import (
	"embed"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/web"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Handlers serve the public marketing/discovery pages.
type Handlers struct {
	*web.Env
	Auth *auth.Service
}

// New builds the marketing handlers with their own renderer (marketing pages
// composed onto the shared base layout).
func New(deps web.Deps, svc *auth.Service) (*Handlers, error) {
	env, err := web.NewEnv(deps, templatesFS)
	if err != nil {
		return nil, err
	}
	// Root pages get the public topbar by default (handlers needn't pass Layout).
	env.SetDefaultLayout("rootbase")
	return &Handlers{Env: env, Auth: svc}, nil
}

// Routes is the root host's router.
func (s *Handlers) Routes() chi.Router {
	r := chi.NewRouter()
	s.CommonMiddleware(r)
	// Static assets and health don't need a session (and static is hit often), so
	// register them ahead of the session middleware, which does per-request DB work.
	s.SharedRoutes(r)
	// Branded 404 for unrouted paths, run through the session middleware so the
	// renderer can read the (host-only) identity for the page chrome.
	r.NotFound(s.Auth.Sessions.LoadAndSave(s.Auth.ValidateSession(http.HandlerFunc(s.NotFound))).ServeHTTP)

	// Everything below runs the session middleware (the beacon needs it to set this
	// host's minimal identity cookie).
	r.Group(func(r chi.Router) {
		r.Use(s.Auth.Sessions.LoadAndSave)
		r.Use(s.Auth.ValidateSession)
		// Identity beacon: a fresh app sign-in bounces here to drop this host's
		// minimal identity cookie, then forwards back to the app. See
		// docs/auth-cross-host.md.
		r.Get("/session/beacon", s.Auth.BeaconCallback)
		// The root host is the public discovery surface, so it stays crawlable; only
		// the single-use beacon path is disallowed. The two sensitive pages below
		// (per-repeater public pages, personal profiles) stay crawlable but send
		// noindex, so they're dropped from search results rather than blocked.
		r.Get("/robots.txt", web.RobotsTxt("User-agent: *\nDisallow: /session/\n"))
		r.Get("/", s.pageLanding)
		r.Get("/docs", s.pageDocs)                                 // public help / how-it-works
		r.Get("/privacy", s.pagePrivacy)                           // legal: what we collect
		r.Get("/terms", s.pageTerms)                               // legal: terms of use
		r.Get("/orgs", s.pageOrgs)                                 // public organization directory
		r.Get("/orgs/{id}", s.pageOrgPublic)                       // public org page
		r.Get("/orgs/{id}/repeaters", s.pageOrgRepeaters)          // public repeater list + map
		r.Get("/orgs/{id}/repeaters.json", s.orgRepeatersJSON)     // map points (cached), fetched by both pages above
		r.Get("/orgs/{id}/config", s.pageOrgConfig)                // public recommended config
		r.With(web.NoIndex).Get("/r/{id}", s.pageRepeaterPublic)   // public repeater page — not for search
		r.With(web.NoIndex).Get("/u/{username}", s.pageUserPublic) // personal profile — not for search
	})
	return r
}

// pageLanding renders the public marketing page.
func (s *Handlers) pageLanding(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, "landing.html", map[string]any{"Layout": "landingbase"})
}

// pageOrgPublic renders an organization's public page (resolved by slug).
func (s *Handlers) pageOrgPublic(w http.ResponseWriter, r *http.Request) {
	id, ok := s.orgID(r)
	if !ok {
		s.NotFound(w, r)
		return
	}
	org, err := s.Store.GetOrg(r.Context(), id)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	uid := s.Auth.CurrentUserID(r.Context())
	role, isMember, _ := s.Store.OrgRole(r.Context(), id, uid)
	s.renderOrgPublic(w, r, org, isMember, role == "admin")
}

// CustomDomain serves an org's public page when the request arrives on one of
// the org's verified custom domains. Any non-root path is redirected to the app
// on the canonical host. Used as the dispatcher's unknown-host fallback (it
// wraps the app router).
func (s *Handlers) CustomDomain(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := web.HostWithoutPort(r.Host)
		if host == "" || host == "localhost" || strings.EqualFold(host, s.Cfg.PrimaryHost) {
			next.ServeHTTP(w, r)
			return
		}
		org, ok, err := s.Store.OrgByVerifiedDomain(r.Context(), strings.ToLower(host))
		if err != nil || !ok {
			next.ServeHTTP(w, r) // unknown host: serve the app normally
			return
		}
		if r.URL.Path == "/" {
			s.renderOrgPublic(w, r, org, false, false)
			return
		}
		http.Redirect(w, r, s.Origin(r, s.Cfg.PrimaryHost)+r.URL.RequestURI(), http.StatusFound) //nolint:gosec // G710: local path or config-pinned origin
	})
}

// orgID resolves the {id} URL param (a slug) to the internal int64 primary key.
func (s *Handlers) orgID(r *http.Request) (int64, bool) {
	id, err := s.Store.OrgIDBySlug(r.Context(), chi.URLParam(r, "id"))
	return id, err == nil
}
