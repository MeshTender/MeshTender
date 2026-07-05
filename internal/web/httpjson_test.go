package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServeJSONCached(t *testing.T) {
	t.Parallel()
	payload := []map[string]any{{"name": "a", "lat": 1.5, "lon": -2.5}}

	// First (unconditional) request: 200 with the JSON body and caching headers.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x.json", nil)
	if err := ServeJSONCached(rr, req, time.Minute, payload); err != nil {
		t.Fatalf("ServeJSONCached: %v", err)
	}
	res := rr.Result()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Cache-Control"); got != "public, max-age=60" {
		t.Fatalf("Cache-Control = %q", got)
	}
	etag := res.Header.Get("ETag")
	if etag == "" || etag[0] != '"' {
		t.Fatalf("ETag = %q, want a quoted strong validator", etag)
	}
	if len(body) == 0 {
		t.Fatalf("empty body on 200")
	}

	// Conditional request with the matching ETag: 304 and no body.
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/x.json", nil)
	req2.Header.Set("If-None-Match", etag)
	if err := ServeJSONCached(rr2, req2, time.Minute, payload); err != nil {
		t.Fatalf("ServeJSONCached conditional: %v", err)
	}
	res2 := rr2.Result()
	body2, _ := io.ReadAll(res2.Body)
	if res2.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", res2.StatusCode)
	}
	if len(body2) != 0 {
		t.Fatalf("304 body = %q, want empty", body2)
	}
	// The ETag is still advertised on the 304 so the client can keep using it.
	if res2.Header.Get("ETag") != etag {
		t.Fatalf("304 ETag = %q, want %q", res2.Header.Get("ETag"), etag)
	}

	// A stale ETag must not match: the client gets a fresh 200.
	rr3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/x.json", nil)
	req3.Header.Set("If-None-Match", `"deadbeef"`)
	if err := ServeJSONCached(rr3, req3, time.Minute, payload); err != nil {
		t.Fatalf("ServeJSONCached stale: %v", err)
	}
	if rr3.Result().StatusCode != http.StatusOK {
		t.Fatalf("stale-etag status = %d, want 200", rr3.Result().StatusCode)
	}
}
