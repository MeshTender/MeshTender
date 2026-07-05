package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLimitBody: a request body within the cap reads fully, one past the cap
// errors when the handler drains it. Regression for the pre-release audit
// finding that no request-body size limit existed.
func TestLimitBody(t *testing.T) {
	t.Parallel()

	// A handler that drains the body and reports whether the read succeeded.
	var readErr error
	h := limitBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))

	post := func(size int) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", size)))
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	post(maxRequestBody) // exactly at the cap
	if readErr != nil {
		t.Fatalf("body at the cap should read cleanly, got %v", readErr)
	}
	post(maxRequestBody + 1) // one byte over
	if readErr == nil {
		t.Fatal("body over the cap should fail to read, got nil error")
	}
}
