package auth

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// passkeyView is display metadata for one registered passkey.
type passkeyView struct {
	ID      int64
	ShortID string
	Name    string
	Added   time.Time
}

// accountRedirect bounces back to the account page with a flash message under
// the given key ("ok" or "error"), shown in the page-level banner.
func accountRedirect(w http.ResponseWriter, r *http.Request, key, msg string) {
	web.RedirectFlash(w, r, "/account", key, msg)
}

// passkeyRedirect bounces back to the account page with a flash shown inside the
// Passkeys card ("pk" for success, "pkerr" for failure), so passkey messages
// stay next to the passkey controls rather than in the top banner.
func passkeyRedirect(w http.ResponseWriter, r *http.Request, key, msg string) {
	web.RedirectFlash(w, r, "/account", key, msg)
}

// pageAccount renders the current user's account-settings page.
func (s *Handlers) pageAccount(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	u, err := s.Store.GetUserByID(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load account", http.StatusInternalServerError)
		return
	}
	creds, err := s.Store.ListCredentials(r.Context(), uid)
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
		views = append(views, passkeyView{ID: c.ID, ShortID: short, Name: c.Name, Added: c.CreatedAt})
	}
	// When set, the user changed their username recently and must wait until
	// this time before changing it again.
	nextRename, err := s.Store.NextRenameAllowed(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load account", http.StatusInternalServerError)
		return
	}
	s.Render(w, r, "account.html", map[string]any{
		"User":        u,
		"DisplayName": u.DisplayName, // nil when unset
		"HasPassword": u.PasswordHash != nil,
		"Passkeys":    views,
		"NextRename":  nextRename, // nil when a rename is allowed now
		"Error":       r.URL.Query().Get("error"),
		"OK":          r.URL.Query().Get("ok"),
		"PKMsg":       r.URL.Query().Get("pk"),
		"PKErr":       r.URL.Query().Get("pkerr"),
	})
}

// handleChangeUsername renames the current user, enforcing validation, the
// per-user rename interval, and the release cooldown on names others hold.
func (s *Handlers) handleChangeUsername(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)
	newName := NormalizeUsername(r.FormValue("username"))
	if !ValidUsername(newName) {
		accountRedirect(w, r, "error", "Choose a username 3–32 characters long, using only letters, digits, and _ . -")
		return
	}
	meta := store.UsernameChangeContext{ChangedBy: uid, IP: web.ClientIP(r), UserAgent: r.UserAgent()}
	err := s.Store.SetUsername(ctx, uid, newName, meta, true)
	switch {
	case errors.Is(err, store.ErrDuplicate), errors.Is(err, store.ErrUsernameReserved):
		// Collapse "taken" and "reserved" into one message so we don't reveal
		// that a name was previously in use by someone else.
		accountRedirect(w, r, "error", "That username isn't available. Please choose another.")
	case errors.Is(err, store.ErrRenameTooSoon):
		accountRedirect(w, r, "error", "You can only change your username once every 30 days.")
	case err != nil:
		accountRedirect(w, r, "error", "Could not change your username.")
	default:
		accountRedirect(w, r, "ok", "Username changed to @"+newName+".")
	}
}

// handleUpdateProfile saves the user's display name.
func (s *Handlers) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	displayName := NormalizeDisplayName(r.FormValue("display_name"))
	if err := s.Store.SetDisplayName(r.Context(), uid, displayName); err != nil {
		accountRedirect(w, r, "error", "Could not save your profile.")
		return
	}
	accountRedirect(w, r, "ok", "Profile updated.")
}

// handleChangePassword sets, changes, or removes the user's password.
func (s *Handlers) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)
	u, err := s.Store.GetUserByID(ctx, uid)
	if err != nil {
		accountRedirect(w, r, "error", "Could not load account.")
		return
	}

	if r.FormValue("remove") == "1" {
		if u.PasswordHash == nil {
			accountRedirect(w, r, "error", "No password to remove.")
			return
		}
		n, err := s.Store.CountCredentials(ctx, uid)
		if err != nil {
			accountRedirect(w, r, "error", "Could not load passkeys.")
			return
		}
		if n < 1 {
			accountRedirect(w, r, "error", "Add a passkey before removing your password — it's your only way to sign in.")
			return
		}
		if err := s.Store.ClearPassword(ctx, uid); err != nil {
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
	if u.PasswordHash != nil && !s.Auth.PasswordMatches(u, r.FormValue("current_password")) {
		accountRedirect(w, r, "error", "Current password is incorrect.")
		return
	}
	if err := s.Auth.SetPassword(ctx, uid, newPassword); err != nil {
		accountRedirect(w, r, "error", "Could not update password.")
		return
	}
	accountRedirect(w, r, "ok", "Password updated.")
}

// handleRenamePasskey sets or clears the human-friendly label on one of the
// user's passkeys.
func (s *Handlers) handleRenamePasskey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)
	credID, err := strconv.ParseInt(r.FormValue("credential_id"), 10, 64)
	if err != nil {
		passkeyRedirect(w, r, "pkerr", "Invalid passkey.")
		return
	}
	name := NormalizePasskeyName(r.FormValue("name"))
	if err := s.Store.SetCredentialName(ctx, uid, credID, name); err != nil {
		passkeyRedirect(w, r, "pkerr", "Could not rename that passkey.")
		return
	}
	passkeyRedirect(w, r, "pk", "Passkey name saved.")
}

// handleDeletePasskey removes one of the user's passkeys, refusing to remove
// the last sign-in method.
func (s *Handlers) handleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)
	credID, err := strconv.ParseInt(r.FormValue("credential_id"), 10, 64)
	if err != nil {
		passkeyRedirect(w, r, "pkerr", "Invalid passkey.")
		return
	}
	u, err := s.Store.GetUserByID(ctx, uid)
	if err != nil {
		passkeyRedirect(w, r, "pkerr", "Could not load account.")
		return
	}
	n, err := s.Store.CountCredentials(ctx, uid)
	if err != nil {
		passkeyRedirect(w, r, "pkerr", "Could not load passkeys.")
		return
	}
	// After removal the user must retain at least one way to sign in.
	if n-1 < 1 && u.PasswordHash == nil {
		passkeyRedirect(w, r, "pkerr", "You can't remove your only sign-in method. Add another passkey or set a password first.")
		return
	}
	if err := s.Store.DeleteCredential(ctx, uid, credID); err != nil {
		passkeyRedirect(w, r, "pkerr", "Could not remove that passkey.")
		return
	}
	passkeyRedirect(w, r, "pk", "Passkey removed.")
}
