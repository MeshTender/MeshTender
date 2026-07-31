package mail

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	resend "github.com/resend/resend-go/v3"
)

// sendTimeout bounds one provider call. The SDK's own default client allows a full
// minute, which is far too long to hold a request handler open: the person is
// waiting on a page, and a provider that hasn't answered in ten seconds isn't
// about to.
const sendTimeout = 10 * time.Second

// ResendSender delivers through Resend's HTTP API.
type ResendSender struct {
	client  *resend.Client
	from    string
	replyTo string
}

// NewResend builds a sender for the given API key. from must be an address on a
// domain verified with Resend (SPF + DKIM published), or every send is rejected.
// replyTo may be empty.
func NewResend(apiKey, from, replyTo string) *ResendSender {
	// Our own http.Client purely to shorten the timeout; the SDK otherwise handles
	// auth headers and the base URL.
	return &ResendSender{
		client:  resend.NewCustomClient(&http.Client{Timeout: sendTimeout}, apiKey),
		from:    from,
		replyTo: replyTo,
	}
}

// Send delivers m, translating a quota refusal into ErrQuota.
func (s *ResendSender) Send(ctx context.Context, m Message) error {
	req := &resend.SendEmailRequest{
		From:    s.from,
		To:      []string{m.To},
		Subject: m.Subject,
		Text:    m.Text,
		ReplyTo: s.replyTo,
		// Tagged so a send can be attributed to a flow in the provider's own logs
		// without us keeping a delivery table.
		Tags: []resend.Tag{{Name: "kind", Value: string(m.Kind)}},
	}
	opts := &resend.SendEmailOptions{IdempotencyKey: m.IdempotencyKey}
	if _, err := s.client.Emails.SendWithOptions(ctx, req, opts); err != nil {
		// A typed RateLimitError satisfies errors.Is against the SDK's sentinel; we
		// re-flag it as ours so callers don't import the provider to check.
		if errors.Is(err, resend.ErrRateLimit) {
			return fmt.Errorf("%w: %v", ErrQuota, err)
		}
		return fmt.Errorf("resend send (%s): %w", m.Kind, err)
	}
	return nil
}
