package web

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"github.com/jleight/meshtender/internal/auth"
)

// passkeyView is display metadata for one registered passkey.
type passkeyView struct {
	ID      int64
	ShortID string
	Added   time.Time
}

// accountRedirect bounces back to the account page with a flash message under
// the given key ("ok" or "error").
func accountRedirect(w http.ResponseWriter, r *http.Request, key, msg string) {
	redirectFlash(w, r, "/account", key, msg)
}

// pageAccount renders the current user's account-settings page.
func (s *Server) pageAccount(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	u, err := s.store.GetUserByID(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load account", http.StatusInternalServerError)
		return
	}
	creds, err := s.store.ListCredentials(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load passkeys", http.StatusInternalServerError)
		return
	}
	views := make([]passkeyView, 0, len(creds))
	for _, c := range creds {
		short := hex.EncodeToString(c.CredentialID)
		if len(short) > 12 {
			short = short[:12]
		}
		views = append(views, passkeyView{ID: c.ID, ShortID: short, Added: c.CreatedAt})
	}
	s.render(w, r, "account.html", map[string]any{
		"User":        u,
		"DisplayName": u.DisplayName, // nil when unset
		"HasPassword": u.PasswordHash != nil,
		"Passkeys":    views,
		"Error":       r.URL.Query().Get("error"),
		"OK":          r.URL.Query().Get("ok"),
	})
}

// handleUpdateProfile saves the user's display name.
func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	displayName := auth.NormalizeDisplayName(r.FormValue("display_name"))
	if err := s.store.SetDisplayName(r.Context(), uid, displayName); err != nil {
		accountRedirect(w, r, "error", "Could not save your profile.")
		return
	}
	accountRedirect(w, r, "ok", "Profile updated.")
}

// handleChangePassword sets, changes, or removes the user's password.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.auth.CurrentUserID(ctx)
	u, err := s.store.GetUserByID(ctx, uid)
	if err != nil {
		accountRedirect(w, r, "error", "Could not load account.")
		return
	}

	if r.FormValue("remove") == "1" {
		if u.PasswordHash == nil {
			accountRedirect(w, r, "error", "No password to remove.")
			return
		}
		n, err := s.store.CountCredentials(ctx, uid)
		if err != nil {
			accountRedirect(w, r, "error", "Could not load passkeys.")
			return
		}
		if n < 1 {
			accountRedirect(w, r, "error", "Add a passkey before removing your password — it's your only way to sign in.")
			return
		}
		if err := s.store.ClearPassword(ctx, uid); err != nil {
			accountRedirect(w, r, "error", "Could not remove password.")
			return
		}
		accountRedirect(w, r, "ok", "Password removed. You'll sign in with a passkey.")
		return
	}

	newPassword := r.FormValue("new_password")
	if len(newPassword) < 8 {
		accountRedirect(w, r, "error", "New password must be at least 8 characters.")
		return
	}
	// When a password already exists, require the current one to change it.
	if u.PasswordHash != nil && !s.auth.PasswordMatches(u, r.FormValue("current_password")) {
		accountRedirect(w, r, "error", "Current password is incorrect.")
		return
	}
	if err := s.auth.SetPassword(ctx, uid, newPassword); err != nil {
		accountRedirect(w, r, "error", "Could not update password.")
		return
	}
	accountRedirect(w, r, "ok", "Password updated.")
}

// handleDeletePasskey removes one of the user's passkeys, refusing to remove
// the last sign-in method.
func (s *Server) handleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.auth.CurrentUserID(ctx)
	credID, err := strconv.ParseInt(r.FormValue("credential_id"), 10, 64)
	if err != nil {
		accountRedirect(w, r, "error", "Invalid passkey.")
		return
	}
	u, err := s.store.GetUserByID(ctx, uid)
	if err != nil {
		accountRedirect(w, r, "error", "Could not load account.")
		return
	}
	n, err := s.store.CountCredentials(ctx, uid)
	if err != nil {
		accountRedirect(w, r, "error", "Could not load passkeys.")
		return
	}
	// After removal the user must retain at least one way to sign in.
	if n-1 < 1 && u.PasswordHash == nil {
		accountRedirect(w, r, "error", "You can't remove your only sign-in method. Add another passkey or set a password first.")
		return
	}
	if err := s.store.DeleteCredential(ctx, uid, credID); err != nil {
		accountRedirect(w, r, "error", "Could not remove that passkey.")
		return
	}
	accountRedirect(w, r, "ok", "Passkey removed.")
}
