package web

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func cidrs(t *testing.T, ss ...string) []*net.IPNet {
	t.Helper()
	out := make([]*net.IPNet, 0, len(ss))
	for _, s := range ss {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatalf("bad CIDR %q: %v", s, err)
		}
		out = append(out, n)
	}
	return out
}

func TestClientIPFrom(t *testing.T) {
	loopback := cidrs(t, "127.0.0.0/8")
	withRouter := cidrs(t, "127.0.0.0/8", "192.168.3.1/32")

	cases := []struct {
		name    string
		peer    string
		xff     string
		xRealIP string
		trusted []*net.IPNet
		want    string
	}{
		{
			name: "untrusted peer ignores headers (anti-spoof)",
			peer: "203.0.113.9:443", xff: "1.2.3.4", trusted: loopback,
			want: "203.0.113.9",
		},
		{
			name: "local proxy + router in XFF, router NOT trusted -> logs router (the bug)",
			peer: "127.0.0.1:5000", xff: "203.0.113.9, 192.168.3.1", trusted: loopback,
			want: "192.168.3.1",
		},
		{
			name: "local proxy + router trusted -> recovers real client",
			peer: "127.0.0.1:5000", xff: "203.0.113.9, 192.168.3.1", trusted: withRouter,
			want: "203.0.113.9",
		},
		{
			name: "trusted peer, no XFF, X-Real-IP used",
			peer: "127.0.0.1:5000", xRealIP: "203.0.113.9", trusted: loopback,
			want: "203.0.113.9",
		},
		{
			name: "trusted peer, all hops trusted -> falls back to peer",
			peer: "127.0.0.1:5000", xff: "192.168.3.1", trusted: withRouter,
			want: "127.0.0.1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClientIPFrom(tc.peer, tc.xff, tc.xRealIP, tc.trusted); got != tc.want {
				t.Errorf("ClientIPFrom(peer=%q, xff=%q, xRealIP=%q) = %q, want %q",
					tc.peer, tc.xff, tc.xRealIP, got, tc.want)
			}
		})
	}
}

// TestCaptureRemoteAddr verifies the middleware preserves the true TCP peer in
// the context so RawRemoteAddr can report it after resolution.
func TestCaptureRemoteAddr(t *testing.T) {
	var got string
	h := CaptureRemoteAddr(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = RawRemoteAddr(r)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != "10.0.0.1:5555" {
		t.Errorf("RawRemoteAddr = %q, want 10.0.0.1:5555", got)
	}
}
