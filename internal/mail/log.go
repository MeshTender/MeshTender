package mail

import (
	"context"
	"log/slog"
)

// LogSender writes messages to the log instead of delivering them. It's the
// sender used whenever no provider is configured, which covers local development
// and any test that exercises a flow end to end: the recovery links land in the
// server's own output, so the whole path is walkable with no API key, no verified
// domain, and no network.
//
// It logs the full body on purpose — the body is the only place the link exists,
// and a sender that dropped it silently would be useless for the case it serves.
// That makes it unfit for production, which is why config treats a configured API
// key as the switch away from it.
type LogSender struct {
	// Logger may be nil, in which case the default logger is used.
	Logger *slog.Logger
}

// Send logs m and reports success — the message is "delivered" as far as the
// caller is concerned, so flows behave exactly as they would in production.
func (s *LogSender) Send(_ context.Context, m Message) error {
	l := s.Logger
	if l == nil {
		l = slog.Default()
	}
	l.Info("mail not sent (no provider configured); logging instead",
		"kind", string(m.Kind), "to", m.To, "subject", m.Subject, "body", m.Text)
	return nil
}
