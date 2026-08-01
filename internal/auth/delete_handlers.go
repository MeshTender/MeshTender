package auth

import (
	"errors"
	"net/http"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// Account deletion. The page's job is to make the consequences legible before
// anyone clicks: exactly which repeaters would be destroyed (with a link to hand
// each one to a steward instead), which organizations go or stay, and anything
// that blocks the deletion outright.

// deleteAccountErr bounces back to the confirm page with an error, so the user
// keeps the context rather than landing on the account page wondering.
func deleteAccountErr(w http.ResponseWriter, r *http.Request, msg string) {
	web.RedirectErr(w, r, "/account/delete", msg)
}

// pageDeleteAccount renders the deletion confirm page.
func (s *Handlers) pageDeleteAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)
	u, err := s.Store.GetUserByID(ctx, uid)
	if err != nil {
		s.ServerError(w, r, "could not load account", err)
		return
	}
	preview, err := s.Store.PreviewUserDeletion(ctx, uid)
	if err != nil {
		s.ServerError(w, r, "could not load account", err)
		return
	}
	s.Render(w, r, "delete_account.html", map[string]any{
		"User":    u,
		"Preview": preview,
		// Which proof of identity to ask for. An account can have both; the password
		// field is shown when there is one, with the passkey button alongside.
		"HasPassword": u.PasswordHash != nil,
		"HasPasskeys": preview.Passkeys > 0,
		// Repeater transfer lives on the app host, so links out need its origin.
		"AppOrigin": s.Auth.AppOrigin(r),
		"Error":     r.URL.Query().Get("error"),
	})
}

// handleDeleteAccount verifies the person is still present — a password, or a
// passkey assertion completed in the last ReauthWindow — and then deletes the
// account. The store re-checks every blocker inside its transaction, so this
// handler's job is the identity proof and the messaging.
func (s *Handlers) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)
	u, err := s.Store.GetUserByID(ctx, uid)
	if err != nil {
		s.ServerError(w, r, "could not load account", err)
		return
	}

	// A fresh passkey assertion satisfies this outright; otherwise fall back to the
	// password. An account with neither can't reach here — every account has at
	// least one sign-in method, enforced when removing one.
	if !s.Auth.ReauthFresh(ctx) {
		switch pw := r.FormValue("password"); {
		case u.PasswordHash == nil:
			deleteAccountErr(w, r, "Verify with your passkey to delete your account.")
			return
		case pw == "":
			deleteAccountErr(w, r, "Enter your password, or verify with your passkey, to delete your account.")
			return
		case !s.Auth.PasswordMatches(u, pw):
			deleteAccountErr(w, r, "That password is incorrect.")
			return
		}
	}

	switch err := s.Store.DeleteUser(ctx, uid); {
	case errors.Is(err, store.ErrSoleOrgAdmin):
		deleteAccountErr(w, r, "You're the only admin of an organization that still has other members. "+
			"Make someone else an admin there first, then come back.")
	case errors.Is(err, store.ErrLastSiteAdmin):
		deleteAccountErr(w, r, "You're the last administrator of this MeshTender instance. "+
			"Give another account the manage-users capability first.")
	case errors.Is(err, store.ErrNotFound):
		// Already gone (a double submit, or deleted in another tab). Treat it as
		// done rather than as an error: the outcome the user asked for holds.
		s.finishDeletion(w, r)
	case err != nil:
		s.ServerError(w, r, "could not delete account", err)
	default:
		s.finishDeletion(w, r)
	}
}

// finishDeletion tears down the session and lands on the sign-in page with a
// confirmation. Every other host drops to anonymous on its next request anyway —
// the logins row cascaded away with the account, and a missing row reads as
// revoked — but clearing this host's session too means the browser isn't left
// holding a cookie for an account that no longer exists.
func (s *Handlers) finishDeletion(w http.ResponseWriter, r *http.Request) {
	_ = s.Auth.Logout(r.Context())
	web.RedirectFlash(w, r, "/login", "ok", "Your account and everything in it have been deleted.")
}
