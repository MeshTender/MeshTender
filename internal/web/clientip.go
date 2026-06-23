package web

import (
	"net"
	"net/http"
	"strings"
)

// resolveClientIP replaces chi's RealIP with trusted-proxy-aware resolution. It
// must run after CaptureRemoteAddr (which preserved the true TCP peer) and sets
// r.RemoteAddr to the resolved client IP so ClientIP and downstream code see it.
func (e *Env) resolveClientIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = ClientIPFrom(
			RawRemoteAddr(r),
			r.Header.Get("X-Forwarded-For"),
			r.Header.Get("X-Real-IP"),
			e.Cfg.TrustedProxies,
		)
		next.ServeHTTP(w, r)
	})
}

// ClientIPFrom resolves the real client IP from the connecting peer and the
// forwarding headers, honoring those headers only through trusted proxies:
//
//   - If the peer is not a trusted proxy, the headers are ignored (they can be
//     spoofed by a direct client) and the peer is returned.
//   - Otherwise the X-Forwarded-For chain is walked right-to-left and the first
//     entry that is not itself a trusted proxy is the client. (X-Real-IP is used
//     as the chain when X-Forwarded-For is absent.)
//   - If every hop is trusted (or there are no headers), the peer is returned.
func ClientIPFrom(peer, xff, xRealIP string, trusted []*net.IPNet) string {
	peerHost := hostOnly(peer)
	if !IsTrustedProxy(peerHost, trusted) {
		return peerHost
	}
	chain := splitList(xff)
	if len(chain) == 0 {
		if x := strings.TrimSpace(xRealIP); x != "" {
			chain = []string{x}
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if !IsTrustedProxy(chain[i], trusted) {
			return chain[i]
		}
	}
	return peerHost
}

// IsTrustedProxy reports whether ip (a bare address, no port) falls within any of
// the trusted CIDR ranges.
func IsTrustedProxy(ip string, trusted []*net.IPNet) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	for _, n := range trusted {
		if n != nil && n.Contains(parsed) {
			return true
		}
	}
	return false
}

func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// splitList splits a comma-separated header value, trimming blanks.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
