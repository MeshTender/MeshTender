package core

import (
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/jleight/meshtender/internal/web"
)

// proxyHeaders are the forwarding-related headers worth surfacing first when
// diagnosing how the reverse proxy presents the client to the app.
var proxyHeaders = []string{
	"X-Forwarded-For",
	"X-Forwarded-Proto",
	"X-Forwarded-Host",
	"X-Forwarded-Port",
	"X-Real-IP",
	"Forwarded",
	"Via",
	"CF-Connecting-IP",
	"True-Client-IP",
	"Fastly-Client-IP",
	"Fly-Client-IP",
	"X-Cluster-Client-IP",
}

// redactedHeaders carry credentials and are masked in the full dump, since this
// page is a prime candidate to be screenshotted while debugging.
var redactedHeaders = map[string]bool{
	"Cookie":              true,
	"Authorization":       true,
	"Proxy-Authorization": true,
}

type headerKV struct {
	Name  string
	Value string
}

// pageProxyTest dumps the request details that determine the client IP the app
// records, so an admin can confirm the reverse proxy is configured correctly.
// chi's RealIP middleware has already rewritten RemoteAddr from the forwarding
// headers by the time this runs; CaptureRemoteAddr preserved the true TCP peer.
func (s *Handlers) pageProxyTest(w http.ResponseWriter, r *http.Request) {
	resolved := web.ClientIP(r)     // what rate-limiting and audit logs record
	rawPeer := web.RawRemoteAddr(r) // the actual connecting socket
	rawHost := rawPeer
	if h, _, err := net.SplitHostPort(rawPeer); err == nil {
		rawHost = h
	}

	// Curated forwarding headers (always shown, blanks included so a missing one
	// is visible).
	forwarding := make([]headerKV, 0, len(proxyHeaders))
	for _, n := range proxyHeaders {
		forwarding = append(forwarding, headerKV{Name: n, Value: r.Header.Get(n)})
	}

	// Full header dump, sorted, with credentials redacted.
	var all []headerKV
	for name, vals := range r.Header {
		v := strings.Join(vals, ", ")
		if redactedHeaders[http.CanonicalHeaderKey(name)] {
			v = "(redacted)"
		}
		all = append(all, headerKV{Name: name, Value: v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	// Trusted-proxy diagnostics: which ranges are trusted, whether the peer is one,
	// and how each X-Forwarded-For hop is classified (the resolved client is the
	// rightmost untrusted hop).
	trusted := s.Cfg.TrustedProxies
	trustedList := make([]string, 0, len(trusted))
	for _, n := range trusted {
		trustedList = append(trustedList, n.String())
	}
	type xffHop struct {
		IP       string
		Trusted  bool
		Selected bool
	}
	var chain []xffHop
	for _, raw := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
		ip := strings.TrimSpace(raw)
		if ip == "" {
			continue
		}
		chain = append(chain, xffHop{IP: ip, Trusted: web.IsTrustedProxy(ip, trusted), Selected: ip == resolved})
	}

	s.Render(w, r, "proxy_test.html", map[string]any{
		"ResolvedIP":     resolved,
		"RawPeer":        rawPeer,
		"PeerTrusted":    web.IsTrustedProxy(rawHost, trusted),
		"TrustedProxies": trustedList,
		"XFFChain":       chain,
		// HeaderApplied is true when a forwarding header changed the recorded IP
		// away from the real peer — i.e. the proxy's headers are being trusted.
		// When false, either this was a direct connection (fine) or the proxy
		// isn't sending forwarding headers (you'd be logging the proxy's IP).
		"HeaderApplied": rawHost != resolved,
		"Forwarding":    forwarding,
		"AllHeaders":    all,
		"Host":          r.Host,
		"Proto":         r.Proto,
		"Scheme":        scheme,
		"Method":        r.Method,
		"RequestURI":    r.RequestURI,
	})
}
