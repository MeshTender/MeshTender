package analytics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jleight/meshtender/internal/config"
)

func testRecorder() *Recorder {
	return New(nil, &config.Config{PrimaryHost: "app.x", RootHost: "x", AuthHost: "auth.x", WWWHost: "www.x"})
}

func TestHandlerRecordsRequest(t *testing.T) {
	rec := testRecorder()
	h := rec.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) }))

	req := httptest.NewRequest(http.MethodGet, "http://app.x/dashboard", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	h.ServeHTTP(httptest.NewRecorder(), req)

	select {
	case e := <-rec.ch:
		if e.Surface != "app" || e.Path != "/dashboard" || e.Status != http.StatusCreated || e.Visitor == "" {
			t.Fatalf("event = %+v, want app /dashboard 201 with a visitor hash", e)
		}
	default:
		t.Fatal("expected an event to be enqueued")
	}
}

// TestRedactPath: the secret invite/share token is templatized so it never
// reaches the raw events table; non-invite paths pass through untouched.
// Regression for the pre-release audit finding that live tokens were recorded.
func TestRedactPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/invite/abc123secret", "/invite/:token"},
		{"/invite/abc123secret/accept", "/invite/:token/accept"},
		{"/invite/", "/invite/"}, // no token, nothing to redact
		{"/invite", "/invite"},
		{"/dashboard", "/dashboard"},
		{"/r/pub-id-not-secret", "/r/pub-id-not-secret"},
		{"/orgs/some-slug", "/orgs/some-slug"},
	}
	for _, c := range cases {
		if got := redactPath(c.in); got != c.want {
			t.Errorf("redactPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestHandlerRedactsInviteToken confirms the redaction happens on the real
// recording path (the enqueued event), not just in the helper.
func TestHandlerRedactsInviteToken(t *testing.T) {
	rec := testRecorder()
	h := rec.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "http://app.x/invite/S3cr3tShareToken", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	h.ServeHTTP(httptest.NewRecorder(), req)

	select {
	case e := <-rec.ch:
		if e.Path != "/invite/:token" {
			t.Fatalf("recorded path = %q, want the token redacted to /invite/:token", e.Path)
		}
	default:
		t.Fatal("expected an event to be enqueued")
	}
}

func TestHandlerSkips(t *testing.T) {
	rec := testRecorder()
	h := rec.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	cases := []struct {
		path, ua string
	}{
		{"/healthz", "Mozilla/5.0"},
		{"/static/app.css", "Mozilla/5.0"},
		{"/repeaters/abc/ws", "Mozilla/5.0"},
		{"/dashboard", "Googlebot/2.1"}, // bot UA
		{"/dashboard", ""},              // empty UA
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "http://app.x"+c.path, nil)
		if c.ua != "" {
			req.Header.Set("User-Agent", c.ua)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		select {
		case e := <-rec.ch:
			t.Fatalf("path %q ua %q should be skipped, got %+v", c.path, c.ua, e)
		default:
		}
	}
}

func TestSurfaceClassification(t *testing.T) {
	rec := testRecorder()
	for host, want := range map[string]string{
		"app.x": "app", "x": "root", "www.x": "root", "auth.x": "auth", "club.example.com": "custom",
	} {
		if got := rec.surface(host); got != want {
			t.Errorf("surface(%q) = %q, want %q", host, got, want)
		}
	}
}

// TestVisitorRotates confirms the visitor hash is stable within identical inputs
// but differs by client (so distinct people are counted distinctly).
func TestVisitorStableAndDistinct(t *testing.T) {
	rec := testRecorder()
	mk := func(ip, ua string) string {
		r := httptest.NewRequest(http.MethodGet, "http://app.x/", nil)
		r.RemoteAddr = ip + ":1234"
		r.Header.Set("User-Agent", ua)
		return rec.visitor(r)
	}
	a1 := mk("1.2.3.4", "UA-A")
	a2 := mk("1.2.3.4", "UA-A")
	b := mk("5.6.7.8", "UA-A")
	if a1 != a2 {
		t.Fatalf("same client should hash the same: %q vs %q", a1, a2)
	}
	if a1 == b {
		t.Fatalf("different clients should hash differently")
	}
}
