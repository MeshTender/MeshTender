// Package mail sends the small set of transactional messages MeshTender needs —
// today the account-recovery pair (verify an address, reset a password).
//
// Callers depend on Sender and Message, never on a provider type. That seam is
// what keeps `go test ./...` off the network: the production binary wires
// ResendSender, dev and tests wire LogSender or a capturing fake.
package mail

import (
	"context"
	"errors"
)

// Kind labels what a message is for. It exists from the first message rather than
// being retrofitted because it's the single place per-category delivery decisions
// will land — a user who wants recovery mail but no activity notifications. Today
// it only tags the provider send and the log line.
type Kind string

const (
	// KindVerifyEmail confirms a newly-set address actually belongs to the account.
	KindVerifyEmail Kind = "verify_email"
	// KindPasswordReset carries a single-use password-reset link.
	KindPasswordReset Kind = "password_reset"
)

// Message is one outbound email.
//
// Text-only, deliberately: these are short, link-bearing transactional messages
// where an HTML part would add a template surface, a second body to keep in sync,
// and a spam-filter signal, all for no reader benefit.
type Message struct {
	To      string
	Subject string
	Text    string
	Kind    Kind
	// IdempotencyKey lets the provider collapse a duplicate send. Callers pass a
	// value derived from the thing the message is about (we use the reset token's
	// hash), so a double-submitted form can't mail the same link twice — which
	// matters both for the reader and for a metered daily quota.
	IdempotencyKey string
}

// Sender delivers a Message. Implementations must respect ctx.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// ErrQuota reports that the provider refused the send because a rate or volume
// limit is exhausted, rather than because the request or the network failed.
// Worth distinguishing: it's the one delivery failure that's a capacity problem
// with a known fix, and it says nothing is wrong with the message itself.
var ErrQuota = errors.New("mail: provider quota exhausted")

// ErrNotConfigured reports that no delivery path is configured. Callers should
// treat this as "this feature is off", not as a transient failure.
var ErrNotConfigured = errors.New("mail: no sender configured")
