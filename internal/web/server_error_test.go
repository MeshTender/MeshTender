package web

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

// captureLogs redirects the default slog logger to a buffer for the duration of
// the test and restores it afterward. LogError/ServerError use the package-level
// slog (wired to slog.SetDefault in main), so this asserts on the real path.
// Not parallel-safe (mutates global default) — callers must not t.Parallel.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := slog.Default()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestServerErrorLogsAndWrites500(t *testing.T) {
	buf := captureLogs(t)

	req := httptest.NewRequest(http.MethodPost, "https://app.example/repeaters", nil)
	// Attach a request ID the way the RequestID middleware would, so the log can
	// be correlated with a user report.
	req = req.WithContext(context.WithValue(req.Context(), middleware.RequestIDKey, "req-abc123"))
	rec := httptest.NewRecorder()

	e := &Env{}
	e.ServerError(rec, req, "could not load repeaters", errors.New("dial tcp: connection refused"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "could not load repeaters") {
		t.Fatalf("body = %q, want the user message", body)
	}

	log := buf.String()
	for _, want := range []string{
		`level=ERROR`,
		`could not load repeaters`,
		`method=POST`,
		`path=/repeaters`,
		`request_id=req-abc123`,
		`connection refused`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\nfull log: %s", want, log)
		}
	}
}

func TestLogErrorAppendsExtraArgs(t *testing.T) {
	buf := captureLogs(t)

	req := httptest.NewRequest(http.MethodGet, "https://app.example/orgs/7", nil)
	LogError(req, "could not load org", errors.New("boom"), "org_id", 7)

	log := buf.String()
	for _, want := range []string{`could not load org`, `err=boom`, `org_id=7`, `path=/orgs/7`} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\nfull log: %s", want, log)
		}
	}
}
