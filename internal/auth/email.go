package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	mailer "github.com/MeshTender/MeshTender/internal/mail"
	"github.com/MeshTender/MeshTender/internal/store"
)

// ErrTooManyEmails reports that an account has asked for more messages of one kind
// than its budget allows. Surfaced to the user as "try again later" — the cap exists
// so nobody can use us to flood a mailbox, and so a metered daily send quota can't
// be drained by one account.
var ErrTooManyEmails = errors.New("auth: too many email requests")

// Per-account send budgets, counted over sendBudgetWindow. Both are generous for a
// real person (a resend after a typo, a second attempt when the first mail is slow)
// and useless for anyone trying to bury an inbox.
const (
	sendBudgetWindow = time.Hour
	maxVerifySends   = 5
	maxResetSends    = 3
)

// mailSendTimeout bounds the provider call a handler waits on.
const mailSendTimeout = 15 * time.Second

// MaxEmailLen bounds a stored address. RFC 5321 caps a path at 256 octets; this is
// the same order and keeps an absurd paste out of the column and the mail body.
const MaxEmailLen = 254

// NormalizeEmailInput trims a submitted address. Case is preserved as typed — the
// user sees their own capitalization back — while all comparison is
// case-insensitive (store.NormalizeEmail).
func NormalizeEmailInput(s string) string { return strings.TrimSpace(s) }

// ValidEmail reports whether addr is a single, plausible address. It leans on
// net/mail's parser rather than a hand-rolled regex, then rejects the display-name
// form ("Someone <a@b>") that ParseAddress also accepts: we store a bare address,
// and letting a display name through would put attacker-chosen text into a
// To: header.
func ValidEmail(addr string) bool {
	if addr == "" || len(addr) > MaxEmailLen {
		return false
	}
	parsed, err := mail.ParseAddress(addr)
	if err != nil {
		return false
	}
	return parsed.Name == "" && parsed.Address == addr
}

// SetAccountEmail stores a new address for the user and mails a confirmation link.
// The address is unverified until that link is clicked, so nothing can be sent to it
// beyond this one message.
func (s *Service) SetAccountEmail(ctx context.Context, r *http.Request, userID int64, addr string) error {
	if err := s.store.SetEmail(ctx, userID, addr); err != nil {
		return err
	}
	return s.SendEmailVerification(ctx, r, userID)
}

// SendEmailVerification mails a fresh confirmation link for the user's current
// address. Used both when an address is first set and when the user asks for a
// resend. A verified address is a no-op — there is nothing to confirm.
func (s *Service) SendEmailVerification(ctx context.Context, r *http.Request, userID int64) error {
	u, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.Email == nil || *u.Email == "" || u.EmailVerifiedAt != nil {
		return nil
	}
	addr := *u.Email

	n, err := s.store.CountRecentEmailTokens(ctx, userID, store.PurposeVerifyEmail, sendBudgetWindow)
	if err != nil {
		return err
	}
	if n >= maxVerifySends {
		return ErrTooManyEmails
	}

	token, err := s.store.CreateEmailToken(ctx, userID, store.PurposeVerifyEmail, addr, store.VerifyTokenTTL)
	if err != nil {
		return err
	}
	link := s.authOrigin(r) + "/verify-email/" + token
	return s.send(ctx, mailer.Message{
		To:      addr,
		Subject: "Confirm your email for MeshTender",
		Kind:    mailer.KindVerifyEmail,
		// Keyed on the token, so a double-submitted form can't mail twice.
		IdempotencyKey: hashForIdempotency(token),
		Text: fmt.Sprintf(`Hi %s,

Confirm this address so it can be used to recover your MeshTender account:

%s

The link works once and expires in %s.

If you didn't add this address to a MeshTender account, ignore this message —
nothing will change, and the address won't be used for anything.
`, atName(u), link, humanDuration(store.VerifyTokenTTL)),
	})
}

// send delivers a message on a context detached from the request's.
//
// The token is already committed by the time we get here, so a visitor who closes
// the tab mid-send must not abort delivery — that would leave a live token whose
// link nobody ever received, and the user would have to start over for no visible
// reason. The timeout keeps the detached call bounded.
func (s *Service) send(ctx context.Context, m mailer.Message) error {
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mailSendTimeout)
	defer cancel()
	if err := s.mail.Send(sendCtx, m); err != nil {
		// Logged here rather than only bubbling up, because the caller deliberately
		// tells the user as little as possible (the reset flow must not confirm
		// whether an address exists), and a quota wall is an operational problem the
		// operator needs to see.
		level := slog.LevelError
		if errors.Is(err, mailer.ErrQuota) {
			level = slog.LevelWarn
		}
		slog.Log(sendCtx, level, "mail send failed", "kind", string(m.Kind), "err", err)
		return err
	}
	return nil
}

// hashForIdempotency derives the provider's de-duplication key from a token. The
// token itself is already in the body we hand the provider, so this isn't about
// secrecy — it's about not also placing a live credential in a header, which
// providers log and retain on different terms than message bodies. Truncated
// because collisions only matter across one account's in-flight sends.
func hashForIdempotency(token string) string {
	sum := sha256.Sum256([]byte("meshtender-idempotency:" + token))
	return hex.EncodeToString(sum[:16])
}

// atName renders a user as "@username" for message bodies. Deliberately the
// username, not the display name: it's what they type to sign in, and the reset mail
// has to name accounts unambiguously when several share one address.
func atName(u *store.User) string { return "@" + u.Username }

// humanDuration renders a TTL the way copy should read it ("24 hours", "45
// minutes"), so the stated shelf life can't drift from the constant that enforces it.
func humanDuration(d time.Duration) string {
	if d >= time.Hour {
		h := int(d.Hours())
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	}
	m := int(d.Minutes())
	if m == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", m)
}
