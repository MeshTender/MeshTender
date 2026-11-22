package core

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/MeshTender/MeshTender/internal/auth"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// Account recovery from the admin side: a user manager issues a single-use link
// that lets someone register a new passkey on their account (see auth.RecoverBegin).
// It's the way back in for a passkey-only account, which email reset deliberately
// can't help. The safeguard is the admin confirming who's asking, so it sits behind
// the manage-users capability, and every link issued is audit-logged.

// recoveryTarget loads the account named in the URL, writing a 404 if there's none.
func (s *Handlers) recoveryTarget(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return nil, false
	}
	u, err := s.Store.GetUserByID(r.Context(), id)
	if err != nil {
		s.NotFound(w, r)
		return nil, false
	}
	return u, true
}

// pageUserRecovery renders the account-recovery modal: how the account signs in
// today, and the form that issues a link. Loaded into the shared modal via htmx.
func (s *Handlers) pageUserRecovery(w http.ResponseWriter, r *http.Request) {
	u, ok := s.recoveryTarget(w, r)
	if !ok {
		return
	}
	passkeys, err := s.Store.CountCredentials(r.Context(), u.ID)
	if err != nil {
		s.ServerError(w, r, "could not load passkeys", err)
		return
	}
	s.Render(w, r, "admin_user_recovery.html", map[string]any{
		"Account":     u,
		"Passkeys":    passkeys,
		"MailEnabled": s.Auth.MailEnabled(),
		"Layout":      "recovery-modal",
	})
}

// handleIssueRecovery creates the link, mails it if it can, and shows it to the
// admin once. It renders the result directly rather than redirecting, so the link
// never sits in a URL, browser history, or a server log.
//
// limit is keyed on the target account. It's here for the same reasons the
// account's own email sends are capped: to stop anyone using us to fill a
// mailbox, and to stop one account using up a metered daily send quota.
func (s *Handlers) handleIssueRecovery(limit web.KeyLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.recoveryTarget(w, r)
		if !ok {
			return
		}
		if !limit.Allow(strconv.FormatInt(u.ID, 10)) {
			web.RedirectErr(w, r, "/admin/users",
				"Too many recovery links for @"+u.Username+" in the last hour. Try again later.")
			return
		}
		link, err := s.Auth.IssueRecoveryLink(r.Context(), r, u.ID, r.FormValue("email"))
		if errors.Is(err, auth.ErrInvalidEmail) {
			web.RedirectErr(w, r, "/admin/users",
				"Enter a single email address, like someone@example.com, or leave it blank.")
			return
		}
		if err != nil {
			s.ServerError(w, r, "could not create recovery link", err)
			return
		}
		if link.MailErr != nil {
			web.LogError(r, "recovery link: mail failed", link.MailErr)
		}
		// Who issued it matters most: the link is only as trustworthy as the check
		// the admin made before sending it.
		web.LogAudit(r, "account recovery link issued",
			"actor_user_id", s.Auth.CurrentUserID(r.Context()),
			"user_id", u.ID, "mailed", link.MailedTo != "")
		s.Render(w, r, "admin_user_recovery.html", map[string]any{
			"Account":    u,
			"Link":       link.URL,
			"MailedTo":   link.MailedTo,
			"MailFailed": link.MailErr != nil,
		})
	}
}
