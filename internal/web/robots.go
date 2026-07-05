package web

import "net/http"

// RobotsDisallowAll is a robots.txt body asking crawlers not to crawl any path.
// Used by the app and auth hosts, which serve no content meant for search.
const RobotsDisallowAll = "User-agent: *\nDisallow: /\n"

// NoIndex sets X-Robots-Tag: noindex so a crawler that fetches the response drops
// it from its index. Used on public pages we don't want surfaced in search
// (per-repeater NFC/QR targets, personal profiles) and blanket on the app/auth
// hosts. The page must stay crawlable (i.e. NOT robots.txt-blocked) for a crawler
// to read this header — blocking and noindex together would hide the header.
func NoIndex(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex")
		next.ServeHTTP(w, r)
	})
}

// RobotsTxt returns a handler that serves a static robots.txt body as text/plain.
func RobotsTxt(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}
}
