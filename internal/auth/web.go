package auth

import (
	"embed"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/web"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Handlers serve the auth host's surface: sign-in/sign-up, the WebAuthn/password
// ceremonies, single logout, and account/credential management. Keeping them on
// the auth host means passkeys and passwords are created on the same origin used
// to sign in.
type Handlers struct {
	*web.Env
	Auth *Service
}

// NewWeb builds the auth surface handlers with their own renderer.
func NewWeb(deps web.Deps, svc *Service) (*Handlers, error) {
	env, err := web.NewEnv(deps, templatesFS)
	if err != nil {
		return nil, err
	}
	return &Handlers{Env: env, Auth: svc}, nil
}

func (s *Handlers) baseMW(r chi.Router) {
	s.CommonMiddleware(r)
	r.Use(s.Auth.Sessions.LoadAndSave)
	r.Use(s.Auth.ValidateSession)
}

// Routes is the auth host's router.
func (s *Handlers) Routes() chi.Router {
	r := chi.NewRouter()
	s.baseMW(r)
	s.SharedRoutes(r)
	s.credentialRoutes(r)
	// Single logout: clears the SSO session (chained to from the app host).
	r.Get("/logout", s.handleAuthLogout)

	// Account/credential management lives here so credentials are created on the
	// same origin used to sign in. Guarded by the SSO session.
	r.Group(func(r chi.Router) {
		r.Use(s.Auth.RequireSSO)
		r.Get("/account", s.pageAccount)
		r.Post("/account/username", s.handleChangeUsername)
		r.Post("/account/profile", s.handleUpdateProfile)
		r.Post("/account/password", s.handleChangePassword)
		r.Post("/account/passkeys/rename", s.handleRenamePasskey)
		r.Post("/account/passkeys/delete", s.handleDeletePasskey)
	})

	// Bare visits to the auth host go to the sign-in page.
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
	return r
}

// credentialRoutes mounts the sign-in/sign-up UI and WebAuthn/password ceremony
// endpoints.
func (s *Handlers) credentialRoutes(r chi.Router) {
	r.Get("/login", s.pageLogin)
	r.Get("/signup", s.pageSignup)
	// Throttle credential submission per client IP to blunt password guessing
	// and signup spam. Allows a burst (e.g. fat-fingered retries), then ~1 try
	// every 6s; bcrypt's cost is the second line of defense.
	authLimit := web.NewRateLimiter(10, 6*time.Second)
	r.With(authLimit.Middleware).Post("/login/password", s.Auth.LoginPassword)
	r.With(authLimit.Middleware).Post("/signup/password", s.Auth.SignupPassword)
	r.Post("/api/register/begin", s.Auth.RegisterBegin)
	r.Post("/api/register/finish", s.Auth.RegisterFinish)
	r.Post("/api/login/begin", s.Auth.LoginBegin)
	r.Post("/api/login/finish", s.Auth.LoginFinish)
	r.Post("/api/login/discoverable/begin", s.Auth.LoginDiscoverableBegin)
	r.Post("/api/login/discoverable/finish", s.Auth.LoginDiscoverableFinish)
}

func (s *Handlers) pageLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.Auth.SetNext(ctx, r.URL.Query().Get("next"))
	s.Auth.SetAuthState(ctx, r.URL.Query().Get("state"))
	// Already signed in (e.g. an existing auth-host SSO session): skip the
	// ceremony and hand straight off, rather than re-prompting.
	if uid := s.Auth.CurrentUserID(ctx); uid != 0 {
		http.Redirect(w, r, s.Auth.PostAuthRedirect(r, uid), http.StatusSeeOther)
		return
	}
	s.Render(w, r, "login.html", map[string]any{
		"Layout": "authbase",
		"Error":  r.URL.Query().Get("error"),
		"Next":   r.URL.Query().Get("next"),
	})
}

func (s *Handlers) pageSignup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.Auth.SetNext(ctx, r.URL.Query().Get("next"))
	s.Auth.SetAuthState(ctx, r.URL.Query().Get("state"))
	if uid := s.Auth.CurrentUserID(ctx); uid != 0 {
		http.Redirect(w, r, s.Auth.PostAuthRedirect(r, uid), http.StatusSeeOther)
		return
	}
	s.Render(w, r, "signup.html", map[string]any{
		"Layout": "authbase",
		"Error":  r.URL.Query().Get("error"),
		"Next":   r.URL.Query().Get("next"),
	})
}

// handleAuthLogout clears the auth host's SSO session and lands the visitor on
// the public root (or the app in narrower configs).
func (s *Handlers) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	_ = s.Auth.Logout(r.Context())
	switch {
	case s.Cfg.RootHost != "":
		http.Redirect(w, r, s.Origin(r, s.Cfg.RootHost)+"/", http.StatusSeeOther)
	case s.Cfg.PrimaryHost != "":
		http.Redirect(w, r, s.Origin(r, s.Cfg.PrimaryHost)+"/", http.StatusSeeOther)
	default:
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}
