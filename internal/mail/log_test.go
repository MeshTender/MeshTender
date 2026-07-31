package mail

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestLogSenderIncludesBody: the body is the only place the recovery link exists,
// so a LogSender that redacted it would break the very case it's for (walking the
// flow locally with no provider configured). Guards against a well-meant
// "don't log secrets" edit that would make dev recovery undebuggable.
func TestLogSenderIncludesBody(t *testing.T) {
	var buf bytes.Buffer
	s := &LogSender{Logger: slog.New(slog.NewTextHandler(&buf, nil))}

	link := "https://auth.example.test/reset/the-token"
	if err := s.Send(context.Background(), Message{
		To:      "someone@example.test",
		Subject: "Reset your password",
		Text:    "Open this link: " + link,
		Kind:    KindPasswordReset,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	out := buf.String()
	for _, want := range []string{link, "someone@example.test", string(KindPasswordReset)} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q\ngot: %s", want, out)
		}
	}
}
