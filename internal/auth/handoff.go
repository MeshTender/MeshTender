package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"time"
)

// The cross-host sign-in handoff. When AuthHost is configured, all credential
// ceremonies run on the auth host; on success it mints a single-use code (see
// store.CreateAuthCode) and redirects the browser to the app host's
// SessionCallback, which redeems the code and establishes the app host's own
// host-scoped session. A CSRF "state" nonce, set as a host-only cookie on the
// app host before the bounce and echoed back through the auth host, ties the
// returning callback to the request that started it (blocks login-CSRF).

const (
	sessKeyState     = "auth_state"     // string: CSRF nonce echoed through the auth host
	sessKeyAuthLocal = "auth_local"     // bool: this login should return to an auth-host page, not hand off
	stateCookie      = "mt_state"       // base name; __Host- prefixed over HTTPS
	stateMaxAge      = 10 * time.Minute // window to complete a sign-in
	maxStateLen      = 256              // bound stored/echoed state length
)

// SplitHost returns true when this Service runs the auth front door on a
// separate host from the app (cross-host handoff mode).
func (s *Service) SplitHost() bool { return s.authHost != "" }

// scheme is the URL scheme matching the cookie Secure setting.
func (s *Service) scheme() string {
	if s.secure {
		return "https"
	}
	return "http"
}

// portSuffix returns the ":port" from the request host, or "" for default
// ports. The auth and app hosts share one listener, so the app callback reuses
// whatever port the auth request arrived on (covers :8090 dev, bare 443 prod).
func portSuffix(r *http.Request) string {
	if _, port, err := net.SplitHostPort(r.Host); err == nil && port != "" {
		return ":" + port
	}
	return ""
}

func (s *Service) appOrigin(r *http.Request) string {
	return s.scheme() + "://" + s.appHost + portSuffix(r)
}

func (s *Service) authOrigin(r *http.Request) string {
	return s.scheme() + "://" + s.authHost + portSuffix(r)
}

func (s *Service) rootOrigin(r *http.Request) string {
	return s.scheme() + "://" + s.rootHost + portSuffix(r)
}

// SetAuthState stashes the CSRF nonce carried into the auth host so a finisher
// can echo it back to the app callback. Bounded and dropped if empty.
func (s *Service) SetAuthState(ctx context.Context, state string) {
	if state == "" || len(state) > maxStateLen {
		return
	}
	s.Sessions.Put(ctx, sessKeyState, state)
	// A state nonce means this is a cross-host handoff login, not an auth-local
	// one — clear any stale auth-local flag so the handoff isn't suppressed.
	s.Sessions.Remove(ctx, sessKeyAuthLocal)
}

func (s *Service) popAuthState(ctx context.Context) string {
	state, _ := s.Sessions.Get(ctx, sessKeyState).(string)
	s.Sessions.Remove(ctx, sessKeyState)
	return state
}

// PostAuthRedirect is the destination after a successful sign-in. In single-host
// mode it's the stored post-auth path. In split-host mode (on the auth host) it
// mints a handoff code and points at the app host's callback — UNLESS the login
// was initiated for an auth-host-local page (e.g. account settings), in which
// case it returns that local path with no handoff. The caller must already have
// run login() for the auth host's own (SSO) session.
func (s *Service) PostAuthRedirect(r *http.Request, userID int64) string {
	ctx := r.Context()
	next := s.PopNext(ctx)
	// Auth-host-local return (account settings et al.): SSO session is enough,
	// stay on the auth host rather than handing off to the app.
	if s.Sessions.PopBool(ctx, sessKeyAuthLocal) {
		return next
	}
	if !s.SplitHost() || !s.onAuthHost(r) {
		return next
	}
	// Thread this host's login row into the code so the app callback reuses it
	// instead of minting a second row for the same sign-in.
	code, err := s.store.CreateAuthCode(ctx, userID, s.CurrentLoginID(ctx), next)
	if err != nil {
		// Couldn't mint a code; fall back to the app root rather than stranding
		// the user. They're authenticated on the auth host either way.
		return s.appOrigin(r) + "/"
	}
	q := url.Values{"code": {code}}
	if state := s.popAuthState(ctx); state != "" {
		q.Set("state", state)
	}
	return s.appOrigin(r) + "/session/callback?" + q.Encode()
}

func (s *Service) onAuthHost(r *http.Request) bool {
	return hostOnly(r.Host) == s.authHost
}

// hostOnly strips a trailing :port from a request Host.
func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// StartLogin sends an unauthenticated visitor to the sign-in page (see
// startAuth). next is preserved so they land back where they wanted.
func (s *Service) StartLogin(w http.ResponseWriter, r *http.Request, next string) {
	s.startAuth(w, r, next, "/login")
}

// StartSignup is StartLogin's sibling for account creation: it routes to the
// auth host's sign-up page. Used by the app host's /signup entry and marketing
// "create account" CTAs so signup still flows through the state-protected
// handoff.
func (s *Service) StartSignup(w http.ResponseWriter, r *http.Request, next string) {
	s.startAuth(w, r, next, "/signup")
}

// startAuth begins a sign-in/sign-up. In split-host mode it redirects to the
// auth host's page, first dropping a host-only state cookie on the app host that
// the returning callback must match — which is why auth entry must always go
// through the app host, never a direct link to the auth host. In single-host
// mode it just redirects to the local page.
func (s *Service) startAuth(w http.ResponseWriter, r *http.Request, next, page string) {
	if !SafeLocalPath(next) {
		next = "/"
	}
	if !s.SplitHost() {
		http.Redirect(w, r, page+"?next="+url.QueryEscape(next), http.StatusSeeOther)
		return
	}
	state, err := randomState()
	if err != nil {
		http.Error(w, "could not start sign-in", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(stateCookie, s.secure),
		Value:    state,
		Path:     "/",
		MaxAge:   int(stateMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
	q := url.Values{"next": {next}, "state": {state}}
	http.Redirect(w, r, s.authOrigin(r)+page+"?"+q.Encode(), http.StatusSeeOther)
}

// SessionCallback runs on the app host. It verifies the state nonce against the
// host-only cookie, redeems the single-use handoff code, establishes the app
// host's session, and redirects to the stored post-auth path.
func (s *Service) SessionCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clearStateCookie(w, s.secure)

	state := r.URL.Query().Get("state")
	c, err := r.Cookie(cookieName(stateCookie, s.secure))
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(c.Value)) != 1 {
		s.StartLogin(w, r, "/")
		return
	}

	userID, loginID, next, ok, err := s.store.ConsumeAuthCode(ctx, r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "sign-in failed", http.StatusInternalServerError)
		return
	}
	if !ok {
		s.StartLogin(w, r, "/")
		return
	}
	// Reuse the auth host's login row so a single sign-in stays one revocable row.
	if err := s.loginWithID(ctx, userID, loginID); err != nil {
		http.Error(w, "could not start session", http.StatusInternalServerError)
		return
	}
	if !SafeLocalPath(next) {
		next = "/"
	}
	// Bounce through the root host's beacon so the public discovery surface gets
	// its own minimal identity cookie (and can render logged-in-aware UI), then
	// land on the requested app page. The beacon code carries the same login row
	// and the app-local next; if minting fails we just skip the root cookie this
	// round (discovery renders anonymous until the next sign-in).
	if s.rootHost != "" {
		if code, err := s.store.CreateAuthCode(ctx, userID, loginID, next); err == nil {
			http.Redirect(w, r, s.rootOrigin(r)+"/session/beacon?code="+url.QueryEscape(code), http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// BeaconCallback runs on the root (discovery) host. It redeems a single-use code
// minted by the app host's SessionCallback, drops a minimal host-only identity
// cookie on the root host, then forwards the browser to the originally requested
// app page. A valid code is the only proof of identity accepted here — the root
// host never sets an identity cookie for an unauthenticated caller, which blocks
// login-CSRF/fixation. See docs/auth-cross-host.md.
func (s *Service) BeaconCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, loginID, next, ok, err := s.store.ConsumeAuthCode(ctx, r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "sign-in failed", http.StatusInternalServerError)
		return
	}
	if !SafeLocalPath(next) {
		next = "/"
	}
	// A stale or already-used code just means no root cookie this round — still
	// send the visitor on to the app rather than stranding them.
	if ok {
		_ = s.loginWithID(ctx, userID, loginID)
	}
	http.Redirect(w, r, s.appOrigin(r)+next, http.StatusSeeOther)
}

func clearStateCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(stateCookie, secure),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
