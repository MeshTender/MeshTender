package mail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// serveResend stands in for api.resend.com, capturing the one request the SDK
// makes and replying however the test asks. Pointing the client's BaseURL at it
// keeps these tests offline — no API key, no verified domain, no network.
func serveResend(t *testing.T, status int, body string, capture func(*http.Request, map[string]any)) *ResendSender {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if capture != nil {
			capture(r, payload)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	s := NewResend("re_test_key", "MeshTender <noreply@example.test>", "help@example.test")
	// Trailing slash matters: the SDK resolves the relative path "emails" against
	// this, and without it the last segment would be replaced rather than appended.
	base, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	s.client.BaseURL = base
	return s
}

// TestResendSendMapsMessage pins the translation from our Message onto the
// provider's wire format. It's the whole job of ResendSender, and a silent
// mismatch (wrong field, dropped body) would look like a delivery problem.
func TestResendSendMapsMessage(t *testing.T) {
	var gotReq *http.Request
	var gotBody map[string]any
	s := serveResend(t, http.StatusOK, `{"id":"abc-123"}`, func(r *http.Request, b map[string]any) {
		gotReq, gotBody = r, b
	})

	err := s.Send(context.Background(), Message{
		To:             "someone@example.test",
		Subject:        "Reset your password",
		Text:           "Open this link: https://auth.example.test/reset/tok",
		Kind:           KindPasswordReset,
		IdempotencyKey: "hash-of-token",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotReq.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", gotReq.Method)
	}
	if gotReq.URL.Path != "/emails" {
		t.Errorf("path = %q, want /emails", gotReq.URL.Path)
	}
	if got := gotReq.Header.Get("Authorization"); got != "Bearer re_test_key" {
		t.Errorf("Authorization = %q", got)
	}
	// The reason we pass a key at all: a resubmitted form must not mail the same
	// link twice or spend two of a metered daily quota.
	if got := gotReq.Header.Get("Idempotency-Key"); got != "hash-of-token" {
		t.Errorf("Idempotency-Key = %q, want hash-of-token", got)
	}

	if got := gotBody["from"]; got != "MeshTender <noreply@example.test>" {
		t.Errorf("from = %v", got)
	}
	if got := gotBody["reply_to"]; got != "help@example.test" {
		t.Errorf("reply_to = %v", got)
	}
	to, ok := gotBody["to"].([]any)
	if !ok || len(to) != 1 || to[0] != "someone@example.test" {
		t.Errorf("to = %v, want [someone@example.test]", gotBody["to"])
	}
	if got := gotBody["subject"]; got != "Reset your password" {
		t.Errorf("subject = %v", got)
	}
	if got := gotBody["text"]; got != "Open this link: https://auth.example.test/reset/tok" {
		t.Errorf("text = %v", got)
	}
	// Text-only by design: an html part would be a second body to keep in sync.
	if _, present := gotBody["html"]; present {
		t.Errorf("html part present, want text-only: %v", gotBody["html"])
	}
	tags, ok := gotBody["tags"].([]any)
	if !ok || len(tags) != 1 {
		t.Fatalf("tags = %v, want one entry", gotBody["tags"])
	}
	tag, _ := tags[0].(map[string]any)
	if tag["name"] != "kind" || tag["value"] != string(KindPasswordReset) {
		t.Errorf("tag = %v, want kind=%s", tag, KindPasswordReset)
	}
}

// TestResendSendQuotaError: a 429 must surface as ErrQuota and nothing else, so a
// caller can tell "we're out of daily sends" from "the request was malformed" or
// "the network broke" without importing the provider package.
func TestResendSendQuotaError(t *testing.T) {
	s := serveResend(t, http.StatusTooManyRequests, `{"message":"Too many requests"}`, nil)

	err := s.Send(context.Background(), Message{To: "a@example.test", Kind: KindVerifyEmail})
	if err == nil {
		t.Fatal("Send succeeded, want a quota error")
	}
	if !errors.Is(err, ErrQuota) {
		t.Errorf("error = %v, want it to match ErrQuota", err)
	}
}

// TestResendSendOtherErrorIsNotQuota guards the other half of that split: an
// ordinary API rejection must NOT look like a quota problem, or a permanently
// broken configuration reads as a temporary capacity blip.
func TestResendSendOtherErrorIsNotQuota(t *testing.T) {
	s := serveResend(t, http.StatusUnprocessableEntity,
		`{"message":"The example.test domain is not verified"}`, nil)

	err := s.Send(context.Background(), Message{To: "a@example.test", Kind: KindVerifyEmail})
	if err == nil {
		t.Fatal("Send succeeded, want an error")
	}
	if errors.Is(err, ErrQuota) {
		t.Errorf("error = %v, must not match ErrQuota", err)
	}
}
