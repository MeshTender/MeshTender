package web

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
)

// LiveReloadPath answers with this process's boot ID while live reload is on.
// static/livereload.js polls it and reloads the page when the ID changes, i.e.
// when the dev watcher has restarted the server. Comparing IDs, rather than
// waiting to see the server go away, also catches a restart that happens
// entirely between two polls.
const LiveReloadPath = "/_dev/live-reload"

// WithLiveReload serves LiveReloadPath in front of next when enabled, on every
// host, before the host dispatch. Disabled (production), it returns next
// untouched: the path doesn't exist and no page loads the script.
func WithLiveReload(enabled bool, next http.Handler) http.Handler {
	if !enabled {
		return next
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	bootID := hex.EncodeToString(b)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != LiveReloadPath {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, bootID)
	})
}
