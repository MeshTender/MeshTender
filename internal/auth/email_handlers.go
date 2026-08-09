package auth

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// Flash keys "em" (success) and "emerr" (failure) render inside the account page's
// Email card rather than the top banner, so a message about an address stays next to
// the controls that produced it — the same split the Passkeys card uses.
const (
	flashEmail    = "em"
	flashEmailErr = "emerr"
)

// handleSetEmail stores or clears the account's email address.
//
// Setting an address always mails a confirmation link; until that link is clicked
// the address is unverified and can't be used for recovery. Clearing is immediate
// and takes any outstanding links with it.
func (s *Handlers) handleSetEmail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)

	if r.FormValue("remove") == "1" {
		if err := s.Store.ClearEmail(ctx, uid); err != nil {
			accountRedirect(w, r, flashEmailErr, "Could not remove your email address.")
			return
		}
		accountRedirect(w, r, flashEmail, "Email address removed.")
		return
	}

	addr := NormalizeEmailInput(r.FormValue("email"))
	if !ValidEmail(addr) {
		accountRedirect(w, r, flashEmailErr, "Enter a single email address, like you@example.com.")
		return
	}
	if err := s.Auth.SetAccountEmail(ctx, r, uid, addr); err != nil {
		// The address is stored either way — only the confirmation mail failed — so
		// the message points at the resend button rather than implying nothing
		// happened.
		if errors.Is(err, ErrTooManyEmails) {
			accountRedirect(w, r, flashEmailErr, "Too many confirmation emails requested. Try again in an hour.")
			return
		}
		accountRedirect(w, r, flashEmailErr, "Saved your address, but the confirmation email could not be sent. Try resending it.")
		return
	}
	accountRedirect(w, r, flashEmail, "Check "+addr+" for a link to confirm the address.")
}

// handleResendEmailVerification mails a fresh confirmation link for an address the
// user has already saved.
func (s *Handlers) handleResendEmailVerification(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)

	if err := s.Auth.SendEmailVerification(ctx, r, uid); err != nil {
		if errors.Is(err, ErrTooManyEmails) {
			accountRedirect(w, r, flashEmailErr, "Too many confirmation emails requested. Try again in an hour.")
			return
		}
		accountRedirect(w, r, flashEmailErr, "Could not send the confirmation email. Try again shortly.")
		return
	}
	accountRedirect(w, r, flashEmail, "Confirmation email sent.")
}

// handleVerifyEmail redeems a confirmation link.
//
// Registered without an authentication requirement on purpose: the link is opened
// from a mailbox, which is often a different browser (or a phone) from the one that
// added the address. The token is the proof, and it names exactly one account — so
// requiring a session would only break the common case.
func (s *Handlers) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := chi.URLParam(r, "token")

	tok, ok, err := s.Store.ConsumeEmailToken(ctx, store.PurposeVerifyEmail, token)
	if err != nil {
		s.ServerError(w, r, "could not verify email", err)
		return
	}
	if !ok {
		s.verifyResult(w, r, flashEmailErr, "That confirmation link is invalid or has expired. Sign in and send a new one.")
		return
	}
	// Re-checked against the address the token was issued for, so a link minted for
	// one address can't confirm a different one saved afterwards.
	updated, err := s.Store.MarkEmailVerified(ctx, tok.UserID, tok.Email)
	if err != nil {
		s.ServerError(w, r, "could not verify email", err)
		return
	}
	if !updated {
		s.verifyResult(w, r, flashEmailErr, "That link was for a different address than the one on the account now. Send a new confirmation email.")
		return
	}
	s.verifyResult(w, r, flashEmail, "Email confirmed. You can now reset your password by email if you ever need to.")
}

// verifyResult lands the visitor somewhere sensible after a verification attempt:
// the account page when this browser is signed in (they can see the result in
// context), otherwise the sign-in page, since the link may well have been opened on
// a device with no session.
func (s *Handlers) verifyResult(w http.ResponseWriter, r *http.Request, key, msg string) {
	if s.Auth.CurrentUserID(r.Context()) != 0 {
		accountRedirect(w, r, key, msg)
		return
	}
	loginKey := "ok"
	if key == flashEmailErr {
		loginKey = "error"
	}
	web.RedirectFlash(w, r, "/login", loginKey, msg)
}
