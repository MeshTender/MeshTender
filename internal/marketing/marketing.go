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
	return &Handlers{Env: env, Auth: svc}, nil
}

// Routes is the root host's router.
func (s *Handlers) Routes() chi.Router {
	r := chi.NewRouter()
	s.CommonMiddleware(r)
	r.Use(s.Auth.Sessions.LoadAndSave)
	r.Use(s.Auth.ValidateSession)
	s.SharedRoutes(r)
	// Identity beacon: a fresh app sign-in bounces here to drop this host's
	// minimal identity cookie, then forwards back to the app. See
	// docs/auth-cross-host.md.
	r.Get("/session/beacon", s.Auth.BeaconCallback)
	r.Get("/", s.pageLanding)
	r.Get("/orgs", s.pageOrgs)                        // public organization directory
	r.Get("/orgs/{id}", s.pageOrgPublic)              // public org page
	r.Get("/orgs/{id}/repeaters", s.pageOrgRepeaters) // public repeater list + map
	r.Get("/orgs/{id}/config", s.pageOrgConfig)       // public recommended config
	r.Get("/r/{id}", s.pageRepeaterPublic)            // public repeater page (NFC/QR target)
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
		http.NotFound(w, r)
		return
	}
	org, err := s.Store.GetOrg(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	uid := s.Auth.CurrentUserID(r.Context())
	_, isMember, _ := s.Store.OrgRole(r.Context(), id, uid)
	s.renderOrgPublic(w, r, org, isMember)
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
			s.renderOrgPublic(w, r, org, false)
			return
		}
		http.Redirect(w, r, s.Origin(r, s.Cfg.PrimaryHost)+r.URL.RequestURI(), http.StatusFound)
	})
}

// orgID resolves the {id} URL param (a slug) to the internal int64 primary key.
func (s *Handlers) orgID(r *http.Request) (int64, bool) {
	id, err := s.Store.OrgIDBySlug(r.Context(), chi.URLParam(r, "id"))
	return id, err == nil
}
