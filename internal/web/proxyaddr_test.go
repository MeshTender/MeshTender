package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

// TestCaptureRemoteAddrPreservesPeer verifies the ordering that the proxy-test
// page relies on: CaptureRemoteAddr (which must run before RealIP) keeps the true
// TCP peer, while RealIP rewrites RemoteAddr from X-Forwarded-For so ClientIP
// reflects the header-derived client.
func TestCaptureRemoteAddrPreservesPeer(t *testing.T) {
	var gotClient, gotPeer string
	final := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotClient = ClientIP(r)
		gotPeer = RawRemoteAddr(r)
	})
	// Same order as CommonMiddleware: capture, then RealIP.
	h := CaptureRemoteAddr(middleware.RealIP(final))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:5555" // the proxy/load balancer
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotClient != "203.0.113.9" {
		t.Errorf("ClientIP = %q, want the first X-Forwarded-For entry 203.0.113.9", gotClient)
	}
	if gotPeer != "10.0.0.1:5555" {
		t.Errorf("RawRemoteAddr = %q, want the original peer 10.0.0.1:5555", gotPeer)
	}
}

// TestCaptureRemoteAddrDirect: with no forwarding header, the recorded client IP
// is the peer itself (the "no trusted proxy" case the page warns about).
func TestCaptureRemoteAddrDirect(t *testing.T) {
	var gotClient, gotPeer string
	final := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotClient = ClientIP(r)
		gotPeer = RawRemoteAddr(r)
	})
	h := CaptureRemoteAddr(middleware.RealIP(final))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.7:40000"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotClient != "198.51.100.7" {
		t.Errorf("ClientIP = %q, want peer host 198.51.100.7", gotClient)
	}
	if gotPeer != "198.51.100.7:40000" {
		t.Errorf("RawRemoteAddr = %q, want 198.51.100.7:40000", gotPeer)
	}
}
