package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/jleight/meshtender/internal/store"
)

const (
	sessKeyUserID  = "user_id"  // int64: the authenticated user
	sessKeyLoginID = "login_id" // string: the logins-row id backing this session
	sessKeyWAUID   = "wa_uid"   // int64: user mid-ceremony
	sessKeyWAData  = "wa_data"  // []byte: marshaled webauthn.SessionData
	sessKeyWAName  = "wa_name"  // string: pending passkey name for the in-flight registration
	sessKeyNext    = "next"     // string: post-auth redirect target
)

// Service wires WebAuthn, the data store, and session management.
type Service struct {
	wa       *webauthn.WebAuthn
	store    *store.Store
	Sessions *scs.SessionManager

	// Sign-in happens on the dedicated authHost and hands off to appHost via a
	// single-use code; both are always configured.
	appHost  string
	authHost string
	// rootHost is the public discovery host. When set, a fresh app sign-in
	// bounces through its identity beacon so the root surface can render
	// logged-in-aware UI. Empty ⇒ no beacon. See docs/auth-cross-host.md.
	rootHost string
	secure   bool
}

// Config configures the auth service.
type Config struct {
	RPID          string
	RPDisplayName string
	RPOrigins     []string
	// AppHost serves the product; AuthHost serves the login UI and runs
	// ceremonies, handing off to AppHost. Both are bare hostnames (no
	// scheme/port) and are always set.
	AppHost  string
	AuthHost string
	// RootHost serves public discovery; a fresh app sign-in drops a minimal
	// identity cookie there via its beacon so it can render logged-in-aware UI
	// without sharing a session. Always set.
	RootHost string
	// Secure marks cookies Secure (set false for plain-HTTP localhost dev).
	Secure bool
}

// New constructs the auth Service, including the scs session manager backed by
// the same Postgres pool.
func New(st *store.Store, pool *pgxpool.Pool, cfg Config) (*Service, error) {
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.RPID,
		RPDisplayName: cfg.RPDisplayName,
		RPOrigins:     cfg.RPOrigins,
	})
	if err != nil {
		return nil, err
	}

	sm := scs.New()
	sm.Store = pgxstore.New(pool)
	sm.Lifetime = 7 * 24 * time.Hour
	// Host-only cookie: no Domain attribute, so the auth and app hosts each get
	// their own independent session — the browser never sends one host's cookie
	// to the other. Over HTTPS we use the __Host- prefix, which the browser
	// enforces as host-only + Secure + Path=/ (and blocks subdomain injection).
	// The prefix is illegal over plain HTTP, so dev falls back to a bare name.
	sm.Cookie.Name = cookieName("meshtender_session", cfg.Secure)
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = cfg.Secure
	sm.Cookie.Path = "/"

	return &Service{
		wa:       wa,
		store:    st,
		Sessions: sm,
		appHost:  cfg.AppHost,
		authHost: cfg.AuthHost,
		rootHost: cfg.RootHost,
		secure:   cfg.Secure,
	}, nil
}

// cookieName applies the __Host- prefix over HTTPS (where browsers enforce it)
// and a bare name over plain-HTTP dev (where the prefix is rejected).
func cookieName(base string, secure bool) string {
	if secure {
		return "__Host-" + base
	}
	return base
}

// CurrentUserID returns the authenticated user id from the session, or 0.
func (s *Service) CurrentUserID(ctx context.Context) int64 {
	return s.Sessions.GetInt64(ctx, sessKeyUserID)
}

// login starts a brand-new login: it records a logins row (the cross-host
// source of truth) and binds this host's session to it. Used by the credential
// ceremony finishers. The app host's handoff callback instead reuses the auth
// host's row via loginWithID, so one real sign-in maps to exactly one row.
func (s *Service) login(ctx context.Context, userID int64) error {
	loginID, err := s.store.CreateLogin(ctx, userID)
	if err != nil {
		return err
	}
	return s.loginWithID(ctx, userID, loginID)
}

// loginWithID binds the current host's session to an existing login row and
// rotates the session token to prevent fixation.
func (s *Service) loginWithID(ctx context.Context, userID int64, loginID string) error {
	if err := s.Sessions.RenewToken(ctx); err != nil {
		return err
	}
	s.Sessions.Put(ctx, sessKeyUserID, userID)
	s.Sessions.Put(ctx, sessKeyLoginID, loginID)
	return nil
}

// CurrentLoginID returns the logins-row id backing the session, or "".
func (s *Service) CurrentLoginID(ctx context.Context) string {
	return s.Sessions.GetString(ctx, sessKeyLoginID)
}

// Logout clears this host's session and revokes the shared login row, so every
// other host (auth, root beacon, custom domains) falls to anonymous on its next
// request rather than relying on a redirect chain. See docs/auth-cross-host.md.
func (s *Service) Logout(ctx context.Context) error {
	if loginID := s.Sessions.GetString(ctx, sessKeyLoginID); loginID != "" {
		_ = s.store.RevokeLogin(ctx, loginID)
	}
	return s.Sessions.Destroy(ctx)
}

// ValidateSession invalidates the request's session when its backing login row
// has been revoked (logout elsewhere, "log out everywhere", or an admin action).
// It runs after LoadAndSave on every surface; anonymous and mid-ceremony
// sessions (no login id) pass through untouched. A transient store error
// fails open — the revocation is durable and caught on a later request.
func (s *Service) ValidateSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if loginID := s.Sessions.GetString(ctx, sessKeyLoginID); loginID != "" {
			if _, ok, err := s.store.LoginValid(ctx, loginID); err == nil && !ok {
				_ = s.Sessions.Destroy(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// VerifyPassword checks a username/password pair, returning the user on success.
func (s *Service) VerifyPassword(ctx context.Context, username, password string) (*store.User, error) {
	u, err := s.store.GetUserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if u.PasswordHash == nil {
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(*u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

// SetNext stores a validated post-auth redirect path in the session. Non-local
// paths are ignored.
func (s *Service) SetNext(ctx context.Context, path string) {
	if SafeLocalPath(path) {
		s.Sessions.Put(ctx, sessKeyNext, path)
	}
}

// PopNext returns and clears the stored post-auth redirect, defaulting to "/".
func (s *Service) PopNext(ctx context.Context) string {
	next, _ := s.Sessions.Get(ctx, sessKeyNext).(string)
	s.Sessions.Remove(ctx, sessKeyNext)
	if SafeLocalPath(next) {
		return next
	}
	return "/"
}

// SafeLocalPath reports whether p is a safe same-site redirect target: a
// rooted path that can't escape the origin. It rejects protocol-relative URLs
// ("//host") and the backslash variant ("/\host", which browsers normalize to
// "//host"), as well as any control characters.
func SafeLocalPath(p string) bool {
	if len(p) < 1 || p[0] != '/' {
		return false
	}
	// Block "//host" and "/\host": both resolve to a foreign origin in browsers.
	if len(p) >= 2 && (p[1] == '/' || p[1] == '\\') {
		return false
	}
	// Reject control characters (incl. NUL, tab, CR, LF) that could smuggle
	// headers or confuse URL parsing.
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] == 0x7f {
			return false
		}
	}
	return true
}

// PasswordMatches reports whether the given password matches the user's stored
// hash. It returns false when the user has no password set.
func (s *Service) PasswordMatches(u *store.User, password string) bool {
	if u.PasswordHash == nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(*u.PasswordHash), []byte(password)) == nil
}

// SetPassword hashes and stores a password for a user.
func (s *Service) SetPassword(ctx context.Context, userID int64, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.store.SetPassword(ctx, userID, string(hash))
}

// ErrInvalidCredentials is returned for any failed password authentication.
var ErrInvalidCredentials = errors.New("invalid email or password")
