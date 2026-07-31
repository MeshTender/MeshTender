package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	mailer "github.com/jleight/meshtender/internal/mail"
	"github.com/jleight/meshtender/internal/store"
)

// ErrResetNotAllowed reports that a redeemed reset token can no longer be used to set
// a password, because the account no longer has one. The token was valid; the account
// changed underneath it (someone removed their password after asking for the reset).
// Honouring it anyway would ADD a password to a passkey-only account from a mailbox,
// which is exactly the demotion the whole design refuses.
var ErrResetNotAllowed = errors.New("auth: account is not recoverable by email")

// RequestPasswordReset acts on a "forgot password" submission for identifier, which
// may be either a username or an email address (people forget usernames too).
//
// It returns nil in every case a visitor could observe — unknown address, known
// address, passkey-only account — because the response must never reveal whether an
// account exists. A non-nil error means the send itself failed, which the caller
// reports as a generic problem, not as information about the account.
//
// What actually goes out:
//   - one message per *address*, never one per account, listing every resettable
//     account with its own link (the address column is deliberately not unique, so a
//     personal and an ops account may share one mailbox);
//   - accounts on that address with no password are named as NOT resettable rather
//     than silently omitted — the reader already controls the mailbox, so it leaks
//     nothing, and it answers "why isn't my other account listed";
//   - nothing at all when no verified address matches.
func (s *Service) RequestPasswordReset(ctx context.Context, r *http.Request, identifier string) error {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil
	}

	candidates, err := s.resetCandidates(ctx, identifier)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		// No verified address matches. Send nothing, say nothing.
		return nil
	}
	// Every candidate shares the address (case-insensitively), so one message covers
	// them all. Use the stored form for display and delivery.
	addr := *candidates[0].Email

	var resettable, unrecoverable []*store.User
	for _, u := range candidates {
		if u.PasswordHash != nil {
			resettable = append(resettable, u)
			continue
		}
		unrecoverable = append(unrecoverable, u)
	}

	// Per-account budget, on top of the caller's per-IP and per-address throttles. An
	// account already at its limit is dropped from this message rather than failing
	// the request: the person has recent links in the same mailbox already.
	links := make([]resetLink, 0, len(resettable))
	for _, u := range resettable {
		n, err := s.store.CountRecentEmailTokens(ctx, u.ID, store.PurposeResetPassword, sendBudgetWindow)
		if err != nil {
			return err
		}
		if n >= maxResetSends {
			continue
		}
		token, err := s.store.CreateEmailToken(ctx, u.ID, store.PurposeResetPassword, "", store.ResetTokenTTL)
		if err != nil {
			return err
		}
		links = append(links, resetLink{user: u, url: s.authOrigin(r) + "/reset/" + token, token: token})
	}

	// Everything eligible is over budget: the mail we would send is the mail we
	// already sent. Staying quiet here is the point of the budget.
	if len(links) == 0 && len(resettable) > 0 {
		return nil
	}

	body, key := s.resetMessage(addr, links, unrecoverable)
	return s.send(ctx, mailer.Message{
		To:             addr,
		Subject:        "Reset your MeshTender password",
		Kind:           mailer.KindPasswordReset,
		IdempotencyKey: key,
		Text:           body,
	})
}

// resetLink pairs an account with the single-use URL that resets it.
type resetLink struct {
	user  *store.User
	url   string
	token string
}

// resetCandidates resolves a submitted identifier to the accounts that hold a
// verified address. A username is accepted as well as an address, and either way the
// account must have a confirmed address — there's nowhere else to send the link.
func (s *Service) resetCandidates(ctx context.Context, identifier string) ([]*store.User, error) {
	if strings.Contains(identifier, "@") {
		return s.store.FindUsersByVerifiedEmail(ctx, identifier)
	}
	u, err := s.store.GetUserByUsername(ctx, NormalizeUsername(identifier))
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !u.EmailVerified() {
		return nil, nil
	}
	return []*store.User{u}, nil
}

// resetMessage composes the body and the provider de-duplication key.
//
// The single-account case gets its own wording: the fan-out phrasing ("choose the
// account") reads as a warning sign to anyone who only has one.
func (s *Service) resetMessage(addr string, links []resetLink, unrecoverable []*store.User) (body, idempotencyKey string) {
	var b strings.Builder

	switch {
	case len(links) == 1:
		fmt.Fprintf(&b, `Someone asked to reset the password for the MeshTender account %s.

Open this link to choose a new password:

%s

The link works once and expires in %s.
`, atName(links[0].user), links[0].url, humanDuration(store.ResetTokenTTL))

	case len(links) > 1:
		fmt.Fprintf(&b, `Someone asked to reset a MeshTender password for %s.

More than one account uses this address. Open the link for the account you want:
`, addr)
		for _, l := range links {
			fmt.Fprintf(&b, "\n%s\n%s\n", atName(l.user), l.url)
		}
		fmt.Fprintf(&b, "\nEach link works once and expires in %s.\n",
			humanDuration(store.ResetTokenTTL))

	default:
		// Nothing resettable at all. Explaining beats silence: without this the person
		// re-submits the form forever, and it's the request that generates the support
		// email we're trying to avoid.
		fmt.Fprintf(&b, `Someone asked to reset a MeshTender password for %s.

There's no password to reset here — this address is on an account that signs in with
a passkey only.
`, addr)
	}

	if len(unrecoverable) > 0 && len(links) > 0 {
		b.WriteString("\nThese accounts also use this address but sign in with a passkey and have\nno password, so there's nothing to reset for them:\n")
	}
	for _, u := range unrecoverable {
		fmt.Fprintf(&b, "\n%s\n", atName(u))
	}
	if len(unrecoverable) > 0 {
		b.WriteString(`
To sign in to a passkey-only account, use its passkey. If you've lost the device
that held it, check whether your passkey manager (iCloud Keychain, Google Password
Manager, Windows Hello) synced it to another device — a passkey-only account can't
be recovered by email, deliberately, so that a mailbox alone can never take it over.
`)
	}

	b.WriteString(`
If you didn't ask for this, ignore this message. Nothing has changed and your
existing password still works.
`)

	// Keyed on the tokens in the message, so a resubmitted form can't mail twice —
	// and so a genuinely new request (new tokens) is never mistaken for a duplicate.
	var keySeed strings.Builder
	keySeed.WriteString(addr)
	for _, l := range links {
		keySeed.WriteString(l.token)
	}
	return b.String(), hashForIdempotency(keySeed.String())
}

// PeekResetToken looks up a reset token without spending it, so the form can name the
// account it belongs to. ok is false for an unknown, expired, or already-used token.
func (s *Service) PeekResetToken(ctx context.Context, token string) (*store.User, bool, error) {
	tok, ok, err := s.store.PeekEmailToken(ctx, store.PurposeResetPassword, token)
	if err != nil || !ok {
		return nil, false, err
	}
	u, err := s.store.GetUserByID(ctx, tok.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return u, true, nil
}

// CompletePasswordReset spends a reset token and sets the new password.
//
// Order matters and is the reason this is one function: the token is consumed first
// (atomically, so it can't be spent twice even concurrently), and only then is the
// password written. Callers must validate the password BEFORE calling — a rejected
// password after consumption would burn the link and force the user to start over.
//
// On success every other outstanding reset link for the account dies, and every
// existing login is revoked: if someone else got in with the old password, this is
// the moment they're evicted.
func (s *Service) CompletePasswordReset(ctx context.Context, token, password string) (*store.User, error) {
	tok, ok, err := s.store.ConsumeEmailToken(ctx, store.PurposeResetPassword, token)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}
	u, err := s.store.GetUserByID(ctx, tok.UserID)
	if err != nil {
		return nil, err
	}
	// Re-checked at redemption, not just when the link was minted: the account may
	// have dropped its password in between, and email must never be the thing that
	// puts a password back onto a passkey-only account.
	if u.PasswordHash == nil {
		return nil, ErrResetNotAllowed
	}
	if err := s.SetPassword(ctx, u.ID, password); err != nil {
		return nil, err
	}
	// Best-effort cleanup: the password is already changed, so failing here must not
	// present as a failed reset. Both are re-attempted by the janitor / next login.
	_ = s.store.DeleteEmailTokens(ctx, u.ID, store.PurposeResetPassword)
	_ = s.store.RevokeAllUserLogins(ctx, u.ID)
	return u, nil
}
