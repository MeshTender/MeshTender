package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/jleight/meshtender/internal/store"
)

// RequireUser is middleware that redirects unauthenticated requests to /login.
func (s *Service) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.CurrentUserID(r.Context()) == 0 {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Passkey registration ---

// RegisterBegin starts a passkey registration ceremony. If a user is logged
// in, the passkey is added to that account; otherwise a new account is created
// from the posted email.
func (s *Service) RegisterBegin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var u *store.User
	if uid := s.CurrentUserID(ctx); uid != 0 {
		var err error
		if u, err = s.store.GetUserByID(ctx, uid); err != nil {
			httpError(w, http.StatusInternalServerError, "load user")
			return
		}
	} else {
		username, displayName, ok := readCreds(r)
		if !ok {
			httpError(w, http.StatusBadRequest, "username must be 3–32 chars (letters, digits, _ . -)")
			return
		}
		created, err := s.store.CreateUser(ctx, username, displayName)
		if errors.Is(err, store.ErrDuplicate) {
			httpError(w, http.StatusConflict, "username already taken — choose another or sign in")
			return
		}
		if err != nil {
			httpError(w, http.StatusInternalServerError, "create user")
			return
		}
		u = created
	}

	waUser, err := s.loadWebAuthnUser(ctx, u)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "load credentials")
		return
	}

	options, sessionData, err := s.wa.BeginRegistration(waUser)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "begin registration")
		return
	}
	if err := s.stashCeremony(ctx, u.ID, sessionData); err != nil {
		httpError(w, http.StatusInternalServerError, "save ceremony")
		return
	}
	writeJSON(w, options)
}

// RegisterFinish completes a passkey registration and logs the user in.
func (s *Service) RegisterFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, sessionData, ok := s.popCeremony(ctx)
	if !ok {
		httpError(w, http.StatusBadRequest, "no registration in progress")
		return
	}
	u, err := s.store.GetUserByID(ctx, uid)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "load user")
		return
	}
	waUser, err := s.loadWebAuthnUser(ctx, u)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "load credentials")
		return
	}

	cred, err := s.wa.FinishRegistration(waUser, *sessionData, r)
	if err != nil {
		httpError(w, http.StatusBadRequest, "registration failed: "+err.Error())
		return
	}
	blob, err := json.Marshal(cred)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "marshal credential")
		return
	}
	if err := s.store.AddCredential(ctx, u.ID, cred.ID, blob); err != nil {
		httpError(w, http.StatusInternalServerError, "store credential")
		return
	}
	if err := s.login(ctx, u.ID); err != nil {
		httpError(w, http.StatusInternalServerError, "login")
		return
	}
	writeJSON(w, map[string]any{"ok": true, "redirect": s.PopNext(ctx)})
}

// --- Passkey login ---

// LoginBegin starts a passkey assertion ceremony for the posted username.
func (s *Service) LoginBegin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	username, _, ok := readCreds(r)
	if !ok {
		httpError(w, http.StatusBadRequest, "username required")
		return
	}
	u, err := s.store.GetUserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusUnauthorized, "no such account")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "load user")
		return
	}
	waUser, err := s.loadWebAuthnUser(ctx, u)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "load credentials")
		return
	}
	if len(waUser.creds) == 0 {
		httpError(w, http.StatusBadRequest, "no passkey registered for this account")
		return
	}

	options, sessionData, err := s.wa.BeginLogin(waUser)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "begin login")
		return
	}
	if err := s.stashCeremony(ctx, u.ID, sessionData); err != nil {
		httpError(w, http.StatusInternalServerError, "save ceremony")
		return
	}
	writeJSON(w, options)
}

// LoginFinish completes a passkey assertion and logs the user in.
func (s *Service) LoginFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, sessionData, ok := s.popCeremony(ctx)
	if !ok {
		httpError(w, http.StatusBadRequest, "no login in progress")
		return
	}
	u, err := s.store.GetUserByID(ctx, uid)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "load user")
		return
	}
	waUser, err := s.loadWebAuthnUser(ctx, u)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "load credentials")
		return
	}

	cred, err := s.wa.FinishLogin(waUser, *sessionData, r)
	if err != nil {
		httpError(w, http.StatusUnauthorized, "login failed: "+err.Error())
		return
	}
	// Persist the updated sign counter / clone-warning state.
	if blob, err := json.Marshal(cred); err == nil {
		_ = s.store.UpdateCredential(ctx, cred.ID, blob)
	}
	if err := s.login(ctx, u.ID); err != nil {
		httpError(w, http.StatusInternalServerError, "login")
		return
	}
	writeJSON(w, map[string]any{"ok": true, "redirect": s.PopNext(ctx)})
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
