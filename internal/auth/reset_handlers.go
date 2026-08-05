package auth

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// pageForgot renders the "forgot password" form.
func (s *Handlers) pageForgot(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, "forgot.html", map[string]any{
		"Layout": "authbase",
		"Error":  r.URL.Query().Get("error"),
		// Sent switches the page to the confirmation panel. It's a query flag rather
		// than a distinct URL so the POST can redirect (no re-submit on refresh) and
		// still say the same thing regardless of what was submitted.
		"Sent": r.URL.Query().Get("sent") == "1",
	})
}

// handleForgot acts on a submitted username or email address.
//
// The visitor-visible outcome is identical in every case — address unknown, address
// known, account passkey-only, account over its send budget. That's the whole point:
// a form that answered differently would be an account-enumeration oracle, and this
// one is reachable without signing in.
func (s *Handlers) handleForgot(addrLimit web.KeyLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identifier := NormalizeEmailInput(r.FormValue("identifier"))

		// Keyed on the identifier, not the connection: a per-IP limit alone would let
		// someone bury one person's inbox by rotating addresses. Normalized so case
		// changes don't buy a fresh bucket. Silently treated as "sent" — telling the
		// submitter they hit a limit would confirm the address is worth targeting.
		if identifier != "" && !addrLimit.Allow(store.NormalizeEmail(identifier)) {
			web.LogAudit(r, "password reset throttled per-identifier")
			s.forgotSent(w, r)
			return
		}

		if err := s.Auth.RequestPasswordReset(r.Context(), r, identifier); err != nil {
			// A real delivery failure is the one case worth reporting, and it says
			// nothing about whether the account exists: we'd show it just the same for
			// an address with no account if the provider were down.
			web.LogError(r, "password reset send failed", err)
			web.RedirectFlash(w, r, "/forgot", "error",
				"We couldn't send the email just now. Please try again in a few minutes.")
			return
		}
		s.forgotSent(w, r)
	}
}

// forgotSent is the single response every submission gets.
func (s *Handlers) forgotSent(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/forgot?sent=1", http.StatusSeeOther)
}

// pageReset renders the new-password form for a reset link.
//
// It peeks rather than spends the token: some mail clients and security scanners
// prefetch links, and a GET that consumed the token would hand the user a dead link
// before they ever saw the form.
func (s *Handlers) pageReset(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	u, ok, err := s.Auth.PeekResetToken(r.Context(), token)
	if err != nil {
		s.ServerError(w, r, "could not check reset link", err)
		return
	}
	if !ok {
		s.renderResetInvalid(w, r)
		return
	}
	s.renderReset(w, r, token, u, "")
}

// handleReset sets the new password for a reset link.
func (s *Handlers) handleReset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := chi.URLParam(r, "token")
	password := r.FormValue("new_password")

	// Both checks run BEFORE the token is spent: a correctable mistake — too short,
	// or a mistyped confirmation — must cost the user a correction, not their only
	// link. Whoever is here can't sign in, so a dead link is the expensive failure.
	errMsg := ""
	switch {
	case !ValidPassword(password):
		errMsg = fmt.Sprintf("Password must be at least %d characters.", MinPasswordLen)
	case r.FormValue("confirm_password") != password:
		// Worth confirming here more than anywhere else: it's a value the user can't
		// see, typed by someone already locked out, and the next thing they do with it
		// is sign in.
		errMsg = "The passwords don't match."
	}
	if errMsg != "" {
		u, ok, err := s.Auth.PeekResetToken(ctx, token)
		if err != nil {
			s.ServerError(w, r, "could not check reset link", err)
			return
		}
		if !ok {
			s.renderResetInvalid(w, r)
			return
		}
		s.renderReset(w, r, token, u, errMsg)
		return
	}

	u, err := s.Auth.CompletePasswordReset(ctx, token, password)
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		s.renderResetInvalid(w, r)
		return
	case errors.Is(err, ErrResetNotAllowed):
		// The account shed its password between the link being sent and used, so there
		// is nothing to reset and email must not create one.
		web.RedirectFlash(w, r, "/login", "error",
			"That account no longer has a password — sign in with your passkey instead.")
		return
	case err != nil:
		s.ServerError(w, r, "could not reset password", err)
		return
	}

	web.LogAudit(r, "password reset completed", "user_id", u.ID)
	// Deliberately not signed in here: every login was just revoked, and landing on
	// the sign-in page proves the new password actually works while the user still has
	// it in mind.
	web.RedirectFlash(w, r, "/login", "ok",
		"Password updated. Sign in with your new password.")
}

// renderReset draws the new-password form for a valid token.
func (s *Handlers) renderReset(w http.ResponseWriter, r *http.Request, token string, u *store.User, errMsg string) {
	s.Render(w, r, "reset.html", map[string]any{
		"Layout": "authbase",
		"Token":  token,
		// Named so someone who holds two accounts on one address can see which one
		// they're about to change. Without it they reset the wrong account, can't sign
		// in, and conclude reset is broken.
		"Username":       u.Username,
		"Error":          errMsg,
		"MinPasswordLen": MinPasswordLen,
	})
}

// renderResetInvalid explains a dead link and offers the way forward. Rendered (not
// redirected) so the URL still shows what the visitor clicked.
func (s *Handlers) renderResetInvalid(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, "reset.html", map[string]any{
		"Layout":  "authbase",
		"Invalid": true,
	})
}
