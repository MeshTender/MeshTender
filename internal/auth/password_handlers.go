package auth

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/jleight/meshtender/internal/store"
)

// SignupPassword handles a form-based account creation with a password.
func (s *Service) SignupPassword(w http.ResponseWriter, r *http.Request) {
	username := NormalizeUsername(r.FormValue("username"))
	displayName := NormalizeDisplayName(r.FormValue("display_name"))
	password := r.FormValue("password")
	if !ValidUsername(username) || !ValidPassword(password) {
		redirectErr(w, r, "/signup", "Choose a username (3–32 chars: letters, digits, _ . -) and a password of at least 8 characters.")
		return
	}

	ctx := r.Context()
	u, err := s.store.CreateUser(ctx, username, displayName)
	if errors.Is(err, store.ErrDuplicate) {
		redirectErr(w, r, "/signup", "That username is taken. Choose another.")
		return
	}
	if err != nil {
		redirectErr(w, r, "/signup", "Could not create account.")
		return
	}
	if err := s.SetPassword(ctx, u.ID, password); err != nil {
		redirectErr(w, r, "/signup", "Could not set password.")
		return
	}
	if err := s.login(ctx, u.ID); err != nil {
		redirectErr(w, r, "/signup", "Could not start session.")
		return
	}
	http.Redirect(w, r, s.PostAuthRedirect(r, u.ID), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// LoginPassword handles a form-based password sign-in.
func (s *Service) LoginPassword(w http.ResponseWriter, r *http.Request) {
	username := NormalizeUsername(r.FormValue("username"))
	password := r.FormValue("password")

	u, err := s.VerifyPassword(r.Context(), username, password)
	if err != nil {
		redirectErr(w, r, "/login", "Invalid username or password.")
		return
	}
	if err := s.login(r.Context(), u.ID); err != nil {
		redirectErr(w, r, "/login", "Could not start session.")
		return
	}
	http.Redirect(w, r, s.PostAuthRedirect(r, u.ID), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

func redirectErr(w http.ResponseWriter, r *http.Request, path, msg string) {
	http.Redirect(w, r, path+"?error="+url.QueryEscape(msg), http.StatusSeeOther)
}
