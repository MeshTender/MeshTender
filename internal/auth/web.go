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

// sessionMW loads and validates the SSO session (two DB touches). Applied to the
// route group that needs it — not to static assets or /healthz. It also marks the
// group no-store: a route that can read the session can render user data (and this
// host serves the credential pages), which must never land in a shared or history
// cache (see web.NoStore).
func (s *Handlers) sessionMW(r chi.Router) {
	r.Use(s.Auth.Sessions.LoadAndSave)
	r.Use(s.Auth.ValidateSession)
	r.Use(web.NoStore)
}

// withSession wraps a single handler in the session middleware, for handlers
// registered outside the session route group (e.g. the 404 handler) that render
// page chrome and so need the session in context. It mirrors sessionMW, no-store
// included.
func (s *Handlers) withSession(h http.HandlerFunc) http.HandlerFunc {
	return s.Auth.Sessions.LoadAndSave(s.Auth.ValidateSession(web.NoStore(h))).ServeHTTP
}

// Routes is the auth host's router.
func (s *Handlers) Routes() chi.Router {
	r := chi.NewRouter()
	s.CommonMiddleware(r)
	// Sign-in/account pages are never meant for search. Blanket noindex, and tell
	// crawlers not to crawl the auth host at all.
	r.Use(web.NoIndex)
	r.Get("/robots.txt", web.RobotsTxt(web.RobotsDisallowAll))
	// Static assets and health don't need a session; register them ahead of the
	// session middleware, which does per-request DB work.
	s.SharedRoutes(r)
	r.NotFound(s.withSession(s.NotFound)) // branded 404 (chrome needs the session)

	// Everything below runs the session middleware.
	r.Group(func(r chi.Router) {
		s.sessionMW(r)
		s.credentialRoutes(r)
		// Logout: revokes the login row (dropping every host to anonymous on its next
		// request) and clears this host's SSO session. POST-only — sign-out is a state
		// change, so a forged cross-site GET can't trigger it.
		r.Post("/logout", s.handleAuthLogout)

		// Account/credential management lives here so credentials are created on the
		// same origin used to sign in. Guarded by the SSO session.
		r.Group(func(r chi.Router) {
			r.Use(s.Auth.RequireSSO)
			r.Get("/account", s.pageAccount)
			r.Post("/account/username", s.handleChangeUsername)
			// One endpoint for the whole public profile — display name, text
			// fields, and links are a single form on the account page.
			r.Post("/account/profile", s.handleSaveProfile)
			r.Post("/account/timezone", s.handleSetTimezone)
			r.Post("/account/password", s.handleChangePassword)
			r.Post("/account/email", s.handleSetEmail)
			r.Post("/account/email/verify", s.handleResendEmailVerification)
			r.Post("/account/passkeys/rename", s.handleRenamePasskey)
			r.Post("/account/passkeys/delete", s.handleDeletePasskey)
			r.Get("/account/delete", s.pageDeleteAccount)
			r.Post("/account/delete", s.handleDeleteAccount)
			// Re-auth ceremony for passkey holders confirming a sensitive action.
			// Session-gated (it asserts against the signed-in user's own
			// credentials), so it lives here rather than with the public /api
			// sign-in ceremonies.
			r.Post("/account/reauth/passkey/begin", s.Auth.ReauthPasskeyBegin)
			r.Post("/account/reauth/passkey/finish", s.Auth.ReauthPasskeyFinish)
		})

		// Bare visits to the auth host go to the sign-in page.
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
		})
	})
	return r
}

// credentialRoutes mounts the sign-in/sign-up UI and WebAuthn/password ceremony
// endpoints.
func (s *Handlers) credentialRoutes(r chi.Router) {
	r.Get("/login", s.pageLogin)
	r.Get("/signup", s.pageSignup)
	// Confirming an email address is deliberately session-free: the link is opened
	// from a mailbox, frequently on a different device than the one that added the
	// address. The single-use token names the account, so a session would add nothing
	// but a failure mode. It sits here (inside the session-loading group but outside
	// RequireSSO) so the page chrome still renders signed-in-aware.
	r.Get("/verify-email/{token}", s.handleVerifyEmail)
	// Throttle credential submission per client IP to blunt password guessing
	// and signup spam. Allows a burst (e.g. fat-fingered retries), then ~1 try
	// every 6s; bcrypt's cost is the second line of defense.
	authLimit := web.NewRateLimiter(10, 6*time.Second)
	r.With(authLimit.Middleware).Post("/login/password", s.Auth.LoginPassword)
	r.With(authLimit.Middleware).Post("/signup/password", s.Auth.SignupPassword)
	// The user-initiated WebAuthn "begin" ceremonies share the same per-IP
	// credential-attempt bucket: register/begin persists an account row (unbounded
	// spam would flood the users table and squat usernames) and login/begin is a
	// username oracle. The finish steps require an in-progress ceremony, and
	// discoverable/begin auto-fires on page load (passive autofill) and writes
	// nothing, so those stay unthrottled.
	r.With(authLimit.Middleware).Post("/api/register/begin", s.Auth.RegisterBegin)
	r.Post("/api/register/finish", s.Auth.RegisterFinish)
	r.With(authLimit.Middleware).Post("/api/login/begin", s.Auth.LoginBegin)
	r.Post("/api/login/finish", s.Auth.LoginFinish)
	r.Post("/api/login/discoverable/begin", s.Auth.LoginDiscoverableBegin)
	r.Post("/api/login/discoverable/finish", s.Auth.LoginDiscoverableFinish)
	s.resetRoutes(r)
}

// resetRoutes mounts the password-reset flow, and only when mail is configured:
// without a delivery path, "Forgot password?" would lead to a form that can't do
// anything, which is worse than no link at all.
//
// Both POSTs are throttled on their own per-IP bucket rather than sharing the
// credential-attempt one. They're rare, and each one sends mail — so the budget
// should be tight, and a user fat-fingering the sign-in form shouldn't spend it.
func (s *Handlers) resetRoutes(r chi.Router) {
	if !s.Auth.MailEnabled() {
		return
	}
	resetLimit := web.NewRateLimiter(5, 30*time.Second)
	// The second, independent limit: keyed on the submitted identifier, so rotating
	// source addresses can't turn one person's mailbox into a target. Deliberately
	// slow to refill — three requests, then one every twenty minutes, which is well
	// past what any real "I forgot my password" attempt needs.
	addrLimit := web.NewRateLimiter(3, 20*time.Minute)

	r.Get("/forgot", s.pageForgot)
	r.With(resetLimit.Middleware).Post("/forgot", s.handleForgot(addrLimit))
	// GET peeks the token (never spends it); POST spends it. Session-free, like the
	// email-confirmation link, since the mailbox is often on another device.
	r.Get("/reset/{token}", s.pageReset)
	r.With(resetLimit.Middleware).Post("/reset/{token}", s.handleReset)
}

func (s *Handlers) pageLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.Auth.SetNext(ctx, r.URL.Query().Get("next"))
	s.Auth.SetAuthState(ctx, r.URL.Query().Get("state"))
	// Already signed in (e.g. an existing auth-host SSO session): skip the
	// ceremony and hand straight off, rather than re-prompting.
	if uid := s.Auth.CurrentUserID(ctx); uid != 0 {
		http.Redirect(w, r, s.Auth.PostAuthRedirect(r, uid), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
		return
	}
	s.Render(w, r, "login.html", map[string]any{
		"Layout": "authbase",
		"Error":  r.URL.Query().Get("error"),
		"OK":     r.URL.Query().Get("ok"),
		"Next":   r.URL.Query().Get("next"),
		// Gates the "Forgot password?" link — there's no point offering recovery on a
		// deployment that can't send mail.
		"MailEnabled": s.Auth.MailEnabled(),
	})
}

func (s *Handlers) pageSignup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.Auth.SetNext(ctx, r.URL.Query().Get("next"))
	s.Auth.SetAuthState(ctx, r.URL.Query().Get("state"))
	if uid := s.Auth.CurrentUserID(ctx); uid != 0 {
		http.Redirect(w, r, s.Auth.PostAuthRedirect(r, uid), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
		return
	}
	s.Render(w, r, "signup.html", map[string]any{
		"Layout": "authbase",
		"Error":  r.URL.Query().Get("error"),
		"Next":   r.URL.Query().Get("next"),
		// Stated in the form and used as minlength, so the client hint can't drift
		// from the server's floor.
		"MinPasswordLen": MinPasswordLen,
	})
}

// handleAuthLogout revokes the login row backing this session and lands the
// visitor on the public root. Revoking the shared row is what signs the user out
// of every host (app, root beacon, custom org domains) on their next request; it
// also directly clears an auth-local SSO session that never had an app session.
func (s *Handlers) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	_ = s.Auth.Logout(r.Context())
	s.RedirectAfterLogout(w, r)
}
