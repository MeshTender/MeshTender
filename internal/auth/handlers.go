package auth

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// RequireUser is middleware that sends unauthenticated requests to the auth
// host's sign-in page (with a handoff back to where they were going).
func (s *Service) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.CurrentUserID(r.Context()) == 0 {
			s.StartLogin(w, r, r.URL.RequestURI())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireSSO guards auth-host-local pages (e.g. account settings) against the
// auth host's own SSO session. Unlike RequireUser it does NOT hand off to the
// app afterward: it flags the login as auth-local so PostAuthRedirect returns to
// the requested path on the auth host once the SSO session exists.
func (s *Service) RequireSSO(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if s.CurrentUserID(ctx) == 0 {
			s.Sessions.Put(ctx, sessKeyAuthLocal, true)
			s.SetNext(ctx, r.URL.RequestURI())
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Passkey registration ---

// RegisterBegin starts a passkey registration ceremony. If a user is logged in,
// the passkey is added to that account. Otherwise this begins a NEW account
// signup: the account is NOT persisted yet — we reserve its id (which becomes
// the stable WebAuthn user handle) and stash the pending username, and only
// write the row once a credential is verified at RegisterFinish. That keeps an
// abandoned ceremony from leaving an orphan account that squats the username.
func (s *Service) RegisterBegin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var waUser *webauthnUser
	var name string // passkey label — only when adding to an existing account
	if uid := s.CurrentUserID(ctx); uid != 0 {
		u, err := s.store.GetUserByID(ctx, uid)
		if err != nil {
			httpError(w, r, http.StatusInternalServerError, "load user", err)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		name = NormalizePasskeyName(body.Name)
		// Existing credentials are loaded so the authenticator excludes them.
		if waUser, err = s.loadWebAuthnUser(ctx, u); err != nil {
			httpError(w, r, http.StatusInternalServerError, "load credentials", err)
			return
		}
	} else {
		username, displayName, ok := readCreds(r)
		if !ok {
			httpError(w, r, http.StatusBadRequest, "username must be 3–32 chars (letters, digits, _ . -)", nil)
			return
		}
		// Fail fast on a taken name (authoritatively re-checked at finish's insert).
		if _, err := s.store.GetUserByUsername(ctx, username); err == nil {
			httpError(w, r, http.StatusConflict, "username already taken — choose another or sign in", nil)
			return
		} else if !errors.Is(err, store.ErrNotFound) {
			httpError(w, r, http.StatusInternalServerError, "check username", err)
			return
		}
		reservedID, err := s.store.ReserveUserID(ctx)
		if err != nil {
			httpError(w, r, http.StatusInternalServerError, "reserve account", err)
			return
		}
		// In-memory only — no row is written until finish verifies a credential.
		u := &store.User{ID: reservedID, Username: username}
		if displayName != "" {
			u.DisplayName = &displayName
		}
		waUser = &webauthnUser{user: u}
		s.Sessions.Put(ctx, sessKeyWANewName, username)
		s.Sessions.Put(ctx, sessKeyWANewDN, displayName)
	}

	// Prefer discoverable (resident-key) credentials so the holder can sign in
	// later without typing a username — this is what makes the login page's
	// automatic passkey prompt work.
	options, sessionData, err := s.wa.BeginRegistration(waUser,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
	)
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "begin registration", err)
		return
	}
	if err := s.stashCeremony(ctx, waUser.user.ID, sessionData); err != nil {
		httpError(w, r, http.StatusInternalServerError, "save ceremony", err)
		return
	}
	s.Sessions.Put(ctx, sessKeyWAName, name)
	writeJSON(w, options)
}

// RegisterFinish completes a passkey registration and logs the user in. For a
// deferred new-account ceremony it verifies the credential FIRST, then writes
// the account row (with the reserved id) — so a failed or abandoned ceremony
// never persists an account.
func (s *Service) RegisterFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, sessionData, ok := s.popCeremony(ctx)
	if !ok {
		httpError(w, r, http.StatusBadRequest, "no registration in progress", nil)
		return
	}
	// A pending username marks a new-account ceremony (id reserved, row not yet
	// written); its absence means we're adding a passkey to an existing account.
	newName := s.Sessions.PopString(ctx, sessKeyWANewName)
	newDisplay := s.Sessions.PopString(ctx, sessKeyWANewDN)

	var u *store.User
	var waUser *webauthnUser
	if newName != "" {
		u = &store.User{ID: uid, Username: newName}
		if newDisplay != "" {
			u.DisplayName = &newDisplay
		}
		waUser = &webauthnUser{user: u} // in-memory; no row yet
	} else {
		var err error
		if u, err = s.store.GetUserByID(ctx, uid); err != nil {
			httpError(w, r, http.StatusInternalServerError, "load user", err)
			return
		}
		if waUser, err = s.loadWebAuthnUser(ctx, u); err != nil {
			httpError(w, r, http.StatusInternalServerError, "load credentials", err)
			return
		}
	}

	// Verify the credential before writing anything durable. A failure here is a
	// client/ceremony error (400), but the underlying go-webauthn detail can leak
	// internals, so log it server-side and return only a generic message.
	cred, err := s.wa.FinishRegistration(waUser, *sessionData, r)
	if err != nil {
		web.LogError(r, "webauthn: finish registration", err)
		httpError(w, r, http.StatusBadRequest, "registration failed", nil)
		return
	}
	blob, err := json.Marshal(cred)
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "marshal credential", err)
		return
	}

	if newName != "" {
		// The credential is proven — now persist the account with its reserved id.
		created, err := s.store.CreateUserWithID(ctx, uid, newName, newDisplay)
		if errors.Is(err, store.ErrDuplicate) || errors.Is(err, store.ErrUsernameReserved) {
			httpError(w, r, http.StatusConflict, "username already taken — choose another or sign in", nil)
			return
		}
		if err != nil {
			httpError(w, r, http.StatusInternalServerError, "create account", err)
			return
		}
		u = created
	}

	name := s.Sessions.PopString(ctx, sessKeyWAName)
	if err := s.store.AddCredential(ctx, u.ID, cred.ID, blob, name); err != nil {
		httpError(w, r, http.StatusInternalServerError, "store credential", err)
		return
	}
	if err := s.login(ctx, u.ID); err != nil {
		httpError(w, r, http.StatusInternalServerError, "login", err)
		return
	}
	writeJSON(w, authResult{OK: true, Redirect: s.PostAuthRedirect(r, u.ID)})
}

// --- Passkey login ---

// LoginBegin starts a passkey assertion ceremony for the posted username.
func (s *Service) LoginBegin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	username, _, ok := readCreds(r)
	if !ok {
		httpError(w, r, http.StatusBadRequest, "username required", nil)
		return
	}
	u, err := s.store.GetUserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, r, http.StatusUnauthorized, "no such account", nil)
		return
	}
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "load user", err)
		return
	}
	waUser, err := s.loadWebAuthnUser(ctx, u)
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "load credentials", err)
		return
	}
	if len(waUser.creds) == 0 {
		httpError(w, r, http.StatusBadRequest, "no passkey registered for this account", nil)
		return
	}

	options, sessionData, err := s.wa.BeginLogin(waUser)
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "begin login", err)
		return
	}
	if err := s.stashCeremony(ctx, u.ID, sessionData); err != nil {
		httpError(w, r, http.StatusInternalServerError, "save ceremony", err)
		return
	}
	writeJSON(w, options)
}

// LoginFinish completes a passkey assertion and logs the user in.
func (s *Service) LoginFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, sessionData, ok := s.popCeremony(ctx)
	if !ok {
		httpError(w, r, http.StatusBadRequest, "no login in progress", nil)
		return
	}
	u, err := s.store.GetUserByID(ctx, uid)
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "load user", err)
		return
	}
	waUser, err := s.loadWebAuthnUser(ctx, u)
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "load credentials", err)
		return
	}

	// Assertion failure is a client error (401), but the go-webauthn detail can
	// leak internals — log it server-side and return only a generic message.
	cred, err := s.wa.FinishLogin(waUser, *sessionData, r)
	if err != nil {
		web.LogError(r, "webauthn: finish login", err)
		httpError(w, r, http.StatusUnauthorized, "login failed", nil)
		return
	}
	// Persist the updated sign counter / clone-warning state.
	if blob, err := json.Marshal(cred); err == nil {
		_ = s.store.UpdateCredential(ctx, cred.ID, blob)
	}
	if err := s.login(ctx, u.ID); err != nil {
		httpError(w, r, http.StatusInternalServerError, "login", err)
		return
	}
	writeJSON(w, authResult{OK: true, Redirect: s.PostAuthRedirect(r, u.ID)})
}

// --- usernameless (discoverable) passkey login ---

// LoginDiscoverableBegin starts a passkey assertion ceremony without a known
// user. The browser resolves which discoverable credential to use, so the
// login page can prompt for a passkey before the visitor types anything.
func (s *Service) LoginDiscoverableBegin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	options, sessionData, err := s.wa.BeginDiscoverableLogin()
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "begin login", err)
		return
	}
	blob, err := json.Marshal(sessionData)
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "save ceremony", err)
		return
	}
	// No user is known yet; stash only the session data, clearing any stale uid.
	s.Sessions.Remove(ctx, sessKeyWAUID)
	s.Sessions.Put(ctx, sessKeyWAData, blob)
	writeJSON(w, options)
}

// LoginDiscoverableFinish completes a usernameless assertion, resolving the
// account from the credential's user handle.
func (s *Service) LoginDiscoverableFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	blob, _ := s.Sessions.Get(ctx, sessKeyWAData).([]byte)
	if len(blob) == 0 {
		httpError(w, r, http.StatusBadRequest, "no login in progress", nil)
		return
	}
	s.Sessions.Remove(ctx, sessKeyWAData)
	var sessionData webauthn.SessionData
	if err := json.Unmarshal(blob, &sessionData); err != nil {
		httpError(w, r, http.StatusBadRequest, "invalid ceremony", nil)
		return
	}

	var resolved *store.User
	handler := func(_, userHandle []byte) (webauthn.User, error) {
		if len(userHandle) != 8 {
			return nil, errors.New("unrecognized user handle")
		}
		uid := int64(binary.BigEndian.Uint64(userHandle)) //nolint:gosec // G115: decodes the 8-byte WebAuthn handle encoded from our int64 user ID
		u, err := s.store.GetUserByID(ctx, uid)
		if err != nil {
			return nil, err
		}
		waUser, err := s.loadWebAuthnUser(ctx, u)
		if err != nil {
			return nil, err
		}
		resolved = u
		return waUser, nil
	}

	cred, err := s.wa.FinishDiscoverableLogin(handler, sessionData, r)
	if err != nil || resolved == nil {
		httpError(w, r, http.StatusUnauthorized, "login failed", nil)
		return
	}
	if blob, err := json.Marshal(cred); err == nil {
		_ = s.store.UpdateCredential(ctx, cred.ID, blob)
	}
	if err := s.login(ctx, resolved.ID); err != nil {
		httpError(w, r, http.StatusInternalServerError, "login", err)
		return
	}
	writeJSON(w, authResult{OK: true, Redirect: s.PostAuthRedirect(r, resolved.ID)})
}

// --- ceremony state helpers ---

func (s *Service) stashCeremony(ctx context.Context, userID int64, sd *webauthn.SessionData) error {
	blob, err := json.Marshal(sd)
	if err != nil {
		return err
	}
	s.Sessions.Put(ctx, sessKeyWAUID, userID)
	s.Sessions.Put(ctx, sessKeyWAData, blob)
	return nil
}

func (s *Service) popCeremony(ctx context.Context) (int64, *webauthn.SessionData, bool) {
	uid := s.Sessions.GetInt64(ctx, sessKeyWAUID)
	blob, _ := s.Sessions.Get(ctx, sessKeyWAData).([]byte)
	if uid == 0 || len(blob) == 0 {
		return 0, nil, false
	}
	s.Sessions.Remove(ctx, sessKeyWAUID)
	s.Sessions.Remove(ctx, sessKeyWAData)
	var sd webauthn.SessionData
	if err := json.Unmarshal(blob, &sd); err != nil {
		return 0, nil, false
	}
	return uid, &sd, true
}

// --- small helpers ---

// readCreds parses a JSON body with a username and optional display name,
// returning the normalized username, display name, and whether the username
// is valid.
func readCreds(r *http.Request) (username, displayName string, ok bool) {
	var body struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return "", "", false
	}
	username = NormalizeUsername(body.Username)
	if !ValidUsername(username) {
		return "", "", false
	}
	return username, NormalizeDisplayName(body.DisplayName), true
}

// NormalizeUsername lowercases and trims a username for consistent uniqueness.
func NormalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// NormalizeDisplayName trims and bounds a display name (empty means "unset").
func NormalizeDisplayName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// NormalizePasskeyName trims and bounds a passkey label (empty means "unnamed").
func NormalizePasskeyName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// MinPasswordLen is a basic strength floor. There is deliberately no maximum:
// passwords are pre-hashed before bcrypt (see hashPassword), so bcrypt's 72-byte
// input limit never reaches the user.
const MinPasswordLen = 8

// ValidPassword reports whether p meets the minimum length.
func ValidPassword(p string) bool {
	return len(p) >= MinPasswordLen
}

// ValidUsername reports whether s is 3–32 chars of [a-z0-9_.-].
func ValidUsername(s string) bool {
	if len(s) < 3 || len(s) > 32 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '.', c == '-':
		default:
			return false
		}
	}
	return true
}

// writeJSONStatus writes v as a JSON response with the given status code. A code
// of 0 leaves the default 200 (so the body sets it implicitly on first write).
func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	if code != 0 {
		w.WriteHeader(code)
	}
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, 0, v)
}

// authResult is the JSON body returned after a successful credential ceremony
// (passkey register/login). OK is a legacy success flag; the client actually
// keys off the HTTP status and follows Redirect.
type authResult struct {
	OK       bool   `json:"ok"`
	Redirect string `json:"redirect"`
}

// httpError writes a JSON {"error": msg} with the given status. A server fault
// (5xx) with a non-nil err is logged (keyed by request ID) so it's diagnosable;
// client errors (4xx) are expected and pass err=nil.
func httpError(w http.ResponseWriter, r *http.Request, code int, msg string, err error) {
	if err != nil && code >= http.StatusInternalServerError {
		web.LogError(r, msg, err)
	}
	writeJSONStatus(w, code, map[string]string{"error": msg})
}
