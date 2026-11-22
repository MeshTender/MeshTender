package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	mailer "github.com/MeshTender/MeshTender/internal/mail"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// Account recovery is the way back in for someone whose passkey stopped working.
// Email reset can't help a passkey-only account, on purpose (see
// store.User.CanResetPassword), so a user manager issues a single-use link
// instead, after confirming who's asking. The link lets the holder register a new
// passkey on the existing account. It never sets a password, so a passkey account
// stays a passkey account.

// ErrInvalidEmail reports an address that isn't a single, plausible email address.
var ErrInvalidEmail = errors.New("auth: invalid email address")

// recoverySlot keeps a recovery ceremony apart from every other one, so only
// RecoverFinish can complete it (see ceremonySlot).
var recoverySlot = ceremonySlot{uidKey: sessKeyRecoverUID, dataKey: sessKeyRecoverData}

// RecoveryLink is the outcome of issuing a recovery link.
type RecoveryLink struct {
	// URL is the link itself. The admin sees it once, so they can hand it over
	// another way if mailing it isn't possible.
	URL string
	// MailedTo is the address the link was sent to, or "" if it wasn't mailed.
	MailedTo string
	// MailErr is why mailing failed. The link still works when this is set.
	MailErr error
}

// IssueRecoveryLink creates a recovery link for userID and cancels any earlier one.
//
// With an address, the address is saved to the account (unconfirmed, as with any
// newly set address) and, when mail is set up, the link is mailed there. Using a
// link that came by email proves the mailbox is theirs, so that also confirms the
// address. With no address, nothing is mailed and the admin hands the link over
// themselves.
//
// Callers must only expose this to user managers. It's audit-logged by the caller,
// which knows who's asking.
func (s *Service) IssueRecoveryLink(ctx context.Context, r *http.Request, userID int64, addr string) (RecoveryLink, error) {
	addr = NormalizeEmailInput(addr)
	if addr != "" && !ValidEmail(addr) {
		return RecoveryLink{}, ErrInvalidEmail
	}
	u, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return RecoveryLink{}, err
	}
	// Only touch the stored address when it's actually different, so re-sending a
	// link to someone's existing confirmed address doesn't unconfirm it.
	if addr != "" && (u.Email == nil || store.NormalizeEmail(*u.Email) != store.NormalizeEmail(addr)) {
		if err := s.store.SetEmail(ctx, userID, addr); err != nil {
			return RecoveryLink{}, err
		}
	}

	// The link only confirms an address if it was actually mailed there. A link
	// handed over in person proves nothing about any mailbox.
	mailing := addr != "" && s.mailEnabled
	tokenAddr := ""
	if mailing {
		tokenAddr = addr
	}
	token, err := s.store.ReplaceEmailToken(ctx, userID, store.PurposeRecoverAccount, tokenAddr, store.RecoverTokenTTL)
	if err != nil {
		return RecoveryLink{}, err
	}
	link := RecoveryLink{URL: s.authOrigin(r) + "/recover/" + token}
	if !mailing {
		return link, nil
	}
	link.MailErr = s.send(ctx, mailer.Message{
		To:             addr,
		Subject:        "Get back into your MeshTender account",
		Kind:           mailer.KindAccountRecovery,
		IdempotencyKey: hashForIdempotency(token),
		Text:           recoveryMessage(u, link.URL),
	})
	if link.MailErr == nil {
		link.MailedTo = addr
	}
	return link, nil
}

// recoveryMessage is the body of the recovery email.
func recoveryMessage(u *store.User, url string) string {
	return fmt.Sprintf(`Hi %s,

An administrator made this link so you can get back into your MeshTender account.
Open it to set up a new passkey:

%s

The link works once and expires in %s. After you're signed in, you can remove
any passkey that no longer works from your account page.

If you didn't ask for help signing in, don't open the link. Let the person who
runs your MeshTender server know instead.
`, atName(u), url, web.HumanDuration(store.RecoverTokenTTL))
}

// peekTokenUser looks up the account a token belongs to without spending the token.
// ok is false for an unknown, expired, or already-used token, or one whose account
// is gone.
func (s *Service) peekTokenUser(ctx context.Context, purpose store.TokenPurpose, token string) (*store.User, bool, error) {
	tok, ok, err := s.store.PeekEmailToken(ctx, purpose, token)
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

// PeekRecoveryToken looks up a recovery link without spending it, so the page can
// name the account before anything happens.
func (s *Service) PeekRecoveryToken(ctx context.Context, token string) (*store.User, bool, error) {
	return s.peekTokenUser(ctx, store.PurposeRecoverAccount, token)
}

// recoveryLinkGone is what both ceremony steps say about a link that doesn't work.
const recoveryLinkGone = "This link has expired or has already been used. Ask your administrator for a new one."

// RecoverBegin starts registering a new passkey for the account a recovery link
// belongs to. The link isn't spent here, so a cancelled or failed prompt can be
// tried again with the same link.
func (s *Service) RecoverBegin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Token == "" {
		httpError(w, r, http.StatusBadRequest, recoveryLinkGone, nil)
		return
	}
	u, ok, err := s.PeekRecoveryToken(ctx, body.Token)
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "check recovery link", err)
		return
	}
	if !ok {
		httpError(w, r, http.StatusBadRequest, recoveryLinkGone, nil)
		return
	}

	// The account's existing passkeys are deliberately left out. A passkey that
	// "stopped working" may still be on the device, and listing it would make the
	// browser refuse to create a new one there, which is the whole point of the link.
	options, sessionData, err := s.wa.BeginRegistration(&webauthnUser{user: u},
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
	)
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "begin registration", err)
		return
	}
	if err := s.stashCeremonyIn(ctx, recoverySlot, u.ID, sessionData); err != nil {
		httpError(w, r, http.StatusInternalServerError, "save ceremony", err)
		return
	}
	s.Sessions.Put(ctx, sessKeyRecoverToken, body.Token)
	writeJSON(w, options)
}

// RecoverFinish verifies the new passkey, spends the link, and signs the user in.
//
// The passkey is verified before the link is spent, so a failed prompt leaves the
// link usable. Spending it is atomic, so a link opened in two tabs can only add
// one passkey.
func (s *Service) RecoverFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, sessionData, ok := s.popCeremonyFrom(ctx, recoverySlot)
	token := s.Sessions.PopString(ctx, sessKeyRecoverToken)
	if !ok || token == "" {
		httpError(w, r, http.StatusBadRequest, "no recovery in progress", nil)
		return
	}
	u, err := s.store.GetUserByID(ctx, uid)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, r, http.StatusBadRequest, recoveryLinkGone, nil)
		return
	}
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "load user", err)
		return
	}
	cred, err := s.wa.FinishRegistration(&webauthnUser{user: u}, *sessionData, r)
	if err != nil {
		web.LogError(r, "webauthn: finish recovery registration", err)
		httpError(w, r, http.StatusBadRequest, "Couldn't set up the passkey. Try again.", nil)
		return
	}
	blob, err := json.Marshal(cred)
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "marshal credential", err)
		return
	}

	tok, ok, err := s.store.ConsumeEmailToken(ctx, store.PurposeRecoverAccount, token)
	if err != nil {
		httpError(w, r, http.StatusInternalServerError, "spend recovery link", err)
		return
	}
	// The token must still name the account the ceremony was for. A newer link
	// replaces this one, and replacing it is how an admin cancels a link.
	if !ok || tok.UserID != u.ID {
		httpError(w, r, http.StatusBadRequest, recoveryLinkGone, nil)
		return
	}
	if err := s.store.AddCredential(ctx, u.ID, cred.ID, blob, recoveryAddedLabel); err != nil {
		httpError(w, r, http.StatusInternalServerError, "store credential", err)
		return
	}
	// The link came to this address, so opening it proves the mailbox is theirs.
	// MarkEmailVerified re-checks that the account still holds that address.
	if tok.Email != "" {
		if _, err := s.store.MarkEmailVerified(ctx, u.ID, tok.Email); err != nil {
			web.LogError(r, "recovery: confirm address", err)
		}
	}
	// Sign out everywhere else. If someone else got into the account, this is
	// when they're removed. Best-effort, as after a password reset: the passkey is
	// already added, so a failure here must not look like a failed recovery.
	_ = s.store.RevokeAllUserLogins(ctx, u.ID)
	if err := s.login(ctx, u.ID); err != nil {
		httpError(w, r, http.StatusInternalServerError, "login", err)
		return
	}
	web.LogAudit(r, "account recovery completed", "user_id", u.ID)
	// To the account page rather than through the app handoff: the next thing they
	// should do is remove the passkey that stopped working, and it's listed there.
	writeJSON(w, authResult{OK: true, Redirect: "/account?pk=" + url.QueryEscape(recoveredFlash)})
}

// recoveryAddedLabel labels the passkey a recovery adds, so it's easy to tell
// apart from the one that stopped working. The user can rename it.
const recoveryAddedLabel = "Added during account recovery"

// recoveredFlash is shown on the account page after a recovery.
const recoveredFlash = "You're signed in with your new passkey. If an old passkey no longer works, remove it below."
