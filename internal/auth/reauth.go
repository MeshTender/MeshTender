package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/MeshTender/MeshTender/internal/web"
)

// Re-authentication ("sudo mode"): proving, right now, that the person driving
// an authenticated session is still the account holder. A live session says
// somebody signed in here at some point — not that the person about to destroy
// the account is the one who did. Deleting an account is irreversible and one
// click from an unattended browser, so it demands a fresh proof.
//
// Password holders re-enter their password (handled at the point of use, where
// the form already is); passkey-only accounts complete an assertion here, which
// stamps the session. Either way, the proof expires quickly.

// sessKeyReauthAt holds the unix seconds of the last successful identity proof.
// Stored as an int64 rather than a time.Time: scs serializes session data with
// gob, and an integer needs no type registration to survive a round trip.
const sessKeyReauthAt = "reauth_at"

// ReauthWindow is how long a proof of presence authorizes a sensitive action.
// Long enough to read a confirmation page and think, short enough that a walk-
// away between the ceremony and the click doesn't hand someone the account.
const ReauthWindow = 5 * time.Minute

// MarkReauth records a successful identity proof on the current session.
func (s *Service) MarkReauth(ctx context.Context) {
	s.Sessions.Put(ctx, sessKeyReauthAt, time.Now().Unix())
}

// ReauthFresh reports whether this session proved its identity within
// ReauthWindow.
func (s *Service) ReauthFresh(ctx context.Context) bool {
	at := s.Sessions.GetInt64(ctx, sessKeyReauthAt)
	return at != 0 && time.Since(time.Unix(at, 0)) < ReauthWindow
}

// ReauthPasskeyBegin starts an assertion ceremony against the signed-in user's
// own credentials. Unlike LoginBegin it takes no username: the account is
// whoever the session says it is, so this can't be used to probe for accounts.
func (s *Service) ReauthPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.CurrentUserID(ctx)
	if uid == 0 {
		httpError(w, r, http.StatusUnauthorized, "not signed in", nil)
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
	if len(waUser.creds) == 0 {
		httpError(w, r, http.StatusBadRequest, "no passkey registered for this account", nil)
		return
	}
	options, sessionData, err := s.wa.BeginLogin(waUser)
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "begin verification", err)
		return
	}
	if err := s.stashCeremony(ctx, uid, sessionData); err != nil {
		httpError(w, r, http.StatusInternalServerError, "save ceremony", err)
		return
	}
	writeJSON(w, options)
}

// ReauthPasskeyFinish completes the assertion and stamps the session as freshly
// verified. It grants no new access on its own — the sensitive handler decides
// what a fresh stamp is worth.
func (s *Service) ReauthPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.CurrentUserID(ctx)
	ceremonyUID, sessionData, ok := s.popCeremony(ctx)
	if !ok {
		httpError(w, r, http.StatusBadRequest, "no verification in progress", nil)
		return
	}
	// The ceremony must belong to the session driving it. Begin only ever stashes
	// the current user, so this is belt-and-braces against a ceremony stashed by
	// some other flow (a half-finished sign-in) being spent as a re-auth here.
	if uid == 0 || ceremonyUID != uid {
		httpError(w, r, http.StatusUnauthorized, "verification failed", nil)
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
	cred, err := s.wa.FinishLogin(waUser, *sessionData, r)
	if err != nil {
		// Same treatment as LoginFinish: the go-webauthn detail can leak internals,
		// so log it and return something generic.
		web.LogError(r, "webauthn: finish reauth", err)
		httpError(w, r, http.StatusUnauthorized, "verification failed", nil)
		return
	}
	// Persist the updated sign counter / clone-warning state, as login does.
	if blob, err := json.Marshal(cred); err == nil {
		_ = s.store.UpdateCredential(ctx, cred.ID, blob)
	}
	s.MarkReauth(ctx)
	writeJSON(w, authResult{OK: true})
}

// AppOrigin is the app host's origin for this request, so auth-host pages can
// link into the app (the delete page offers repeater transfers, which live
// there).
func (s *Service) AppOrigin(r *http.Request) string { return s.appOrigin(r) }
