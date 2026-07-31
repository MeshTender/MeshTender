package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/jleight/meshtender/internal/mail"
	"github.com/jleight/meshtender/internal/store"
)

const (
	sessKeyUserID  = "user_id"  // int64: the authenticated user
	sessKeyLoginID = "login_id" // string: the logins-row id backing this session
	sessKeyWAUID   = "wa_uid"   // int64: user (or reserved id) mid-ceremony
	sessKeyWAData  = "wa_data"  // []byte: marshaled webauthn.SessionData
	sessKeyWAName  = "wa_name"  // string: pending passkey name for the in-flight registration
	// Deferred passkey signup: a logged-out register ceremony carries the pending
	// account's username/display name (present only for new-account ceremonies).
	// The account row is written at finish, once a credential is verified.
	sessKeyWANewName = "wa_new_username" // string: pending new-account username
	sessKeyWANewDN   = "wa_new_display"  // string: pending new-account display name
	sessKeyNext      = "next"            // string: post-auth redirect target
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

	// mail delivers account-recovery messages. Never nil — a deployment with no
	// provider configured gets a logging sender — so callers don't nil-check.
	// mailEnabled is the separate question of whether recovery-by-email should be
	// offered in the UI at all; a logging sender says "walkable in dev", not
	// "promise this to users".
	mail        mail.Sender
	mailEnabled bool
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

	// Mail delivers account-recovery messages; nil falls back to a logging sender,
	// which is what dev and tests use. MailEnabled reports whether a real provider
	// is configured, and gates whether the UI offers recovery by email.
	Mail        mail.Sender
	MailEnabled bool
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

	sender := cfg.Mail
	if sender == nil {
		sender = &mail.LogSender{}
	}

	return &Service{
		wa:          wa,
		store:       st,
		Sessions:    sm,
		appHost:     cfg.AppHost,
		authHost:    cfg.AuthHost,
		rootHost:    cfg.RootHost,
		secure:      cfg.Secure,
		mail:        sender,
		mailEnabled: cfg.MailEnabled,
	}, nil
}

// MailEnabled reports whether a real mail provider is configured, so surfaces can
// hide recovery-by-email instead of offering a link that would never arrive.
func (s *Service) MailEnabled() bool { return s.mailEnabled }

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
	// Stamp the sign-in time; best-effort, never blocks login. (Fires on every
	// fresh authentication — password or passkey — not the cross-host handoff,
	// which reuses an existing login row via loginWithID.)
	_ = s.store.TouchLastLogin(ctx, userID)
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
//
// When there's nothing to compare against — no such user, or an account with no
// password (passkey-only) — it still spends the same bcrypt work a real check
// would (see spendPasswordWork) before failing. Returning early instead would make
// the response time an oracle: a real comparison costs ~100ms, an indexed miss
// ~1ms, so an attacker could sort usernames into "exists with a password",
// "exists, passkey-only", and "doesn't exist" by timing alone.
func (s *Service) VerifyPassword(ctx context.Context, username, password string) (*store.User, error) {
	u, err := s.store.GetUserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		spendPasswordWork(password)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if u.PasswordHash == nil {
		spendPasswordWork(password)
		return nil, ErrInvalidCredentials
	}
	ok, legacy := comparePassword(*u.PasswordHash, password)
	if !ok {
		return nil, ErrInvalidCredentials
	}
	// Transparently migrate a legacy (raw-bcrypt) hash to the pre-hash scheme on
	// successful login; best-effort, never blocks the sign-in.
	if legacy {
		_ = s.SetPassword(ctx, u.ID, password)
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
	ok, _ := comparePassword(*u.PasswordHash, password)
	return ok
}

// SetPassword hashes and stores a password for a user.
func (s *Service) SetPassword(ctx context.Context, userID int64, password string) error {
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	return s.store.SetPassword(ctx, userID, hash)
}

// bcrypt only considers the first 72 bytes of its input (and stops at the first
// NUL byte), so we can't feed it a raw password without imposing a length limit.
// Pre-hashing with SHA-256 and base64-encoding the digest collapses any-length
// password to a fixed 44-byte, NUL-free token that always fits — so users face
// no visible maximum. (This is the standard bcrypt-pre-hash construction.)
func prehashPassword(password string) []byte {
	sum := sha256.Sum256([]byte(password))
	enc := base64.StdEncoding.EncodeToString(sum[:])
	return []byte(enc)
}

// hashPassword produces the stored bcrypt hash for a password (pre-hash scheme).
func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword(prehashPassword(password), bcrypt.DefaultCost)
	return string(h), err
}

// dummyPasswordHash is a real bcrypt hash of 32 random bytes, used to burn the
// same work as a genuine verification when there is no stored hash to check. It's
// built at init (not lazily) so the very first login can't be the odd one out, and
// via hashPassword so it is structurally identical to a stored hash — same scheme,
// and automatically the same cost as bcrypt.DefaultCost rather than a hardcoded
// literal that would silently diverge if that constant changed. Nothing a caller
// can submit will match it (the input is 256 bits of randomness that never leaves
// this process).
var dummyPasswordHash = mustDummyPasswordHash()

func mustDummyPasswordHash() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Same treatment as the CSP nonce: a predictable value here would defeat the
		// purpose, and there's no safe fallback.
		panic("dummy password hash: " + err.Error())
	}
	h, err := hashPassword(base64.StdEncoding.EncodeToString(b[:]))
	if err != nil {
		panic("dummy password hash: " + err.Error())
	}
	return h
}

// spendPasswordWork performs the bcrypt work of a failed verification and discards
// the result. It goes through comparePassword — rather than calling bcrypt once
// directly — because comparePassword tries BOTH schemes (pre-hash, then legacy
// raw) and so costs two bcrypt operations on a mismatch. A single direct
// comparison would cost one, leaving a clean 2x timing split between "user exists"
// and "user doesn't", which is the very signal this exists to remove.
func spendPasswordWork(password string) {
	_, _ = comparePassword(dummyPasswordHash, password)
}

// comparePassword checks password against a stored bcrypt hash, accepting both
// the current pre-hash scheme and the legacy raw-bcrypt scheme (hashes written
// before pre-hashing existed). legacy reports which one matched, so the caller
// can upgrade the stored hash. Legacy hashes only ever held passwords ≤72 bytes
// (that limit was enforced then), so the raw comparison is safe and can't be
// confused with a pre-hash match.
func comparePassword(hash, password string) (ok, legacy bool) {
	if bcrypt.CompareHashAndPassword([]byte(hash), prehashPassword(password)) == nil {
		return true, false
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil {
		return true, true
	}
	return false, false
}

// ErrInvalidCredentials is returned for any failed password authentication.
var ErrInvalidCredentials = errors.New("invalid email or password")
