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
	"testing/fstest"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/jleight/meshtender/internal/config"
)

// testEnv builds an Env with a real renderer (shared templates only, no per-surface
// pages, no userInfo) — enough to render the branded error page in a unit test.
func testEnv(t *testing.T) *Env {
	t.Helper()
	cfg := &config.Config{PrimaryHost: "app.example", AuthHost: "auth.example", RootHost: "example"}
	rnd, err := NewRenderer(cfg, fstest.MapFS{})
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	return &Env{Cfg: cfg, Renderer: rnd}
}

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

	e := testEnv(t)
	e.ServerError(rec, req, "could not load repeaters", errors.New("dial tcp: connection refused"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "could not load repeaters") {
		t.Fatalf("body missing the user message: %q", body)
	}
	// It's the branded HTML page, not the internal error detail.
	if !strings.Contains(body, "Something went wrong") {
		t.Fatalf("body is not the branded error page: %q", body)
	}
	if strings.Contains(body, "connection refused") {
		t.Fatalf("branded page leaked the internal error detail: %q", body)
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
