package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ServeJSONCached marshals v to JSON and serves it with a short public cache
// window plus a content-derived ETag, replying 304 Not Modified when the
// client's If-None-Match already matches. It is for small, public, infrequently
// changing payloads (e.g. the org map's repeater points): the ETag lets repeat
// visitors and CDNs revalidate cheaply instead of re-downloading, and max-age
// caps how stale a change can be.
//
// It returns any marshal error before writing a status so the caller can log it
// via ServerError; once headers/body are written it returns nil.
func ServeJSONCached(w http.ResponseWriter, r *http.Request, maxAge time.Duration, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	// A strong, quoted ETag over the exact bytes served (RFC 7232). 128 bits of
	// the digest is ample to make collisions irrelevant here.
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`

	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "public, max-age="+strconv.Itoa(int(maxAge.Seconds())))
	h.Set("ETag", etag)

	if matchesETag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	_, _ = w.Write(b)
	return nil
}

// matchesETag reports whether the If-None-Match header (a comma-separated list,
// possibly "*" or weak "W/"-prefixed validators) matches etag. We compare
// ignoring the weak prefix, which is safe for a cache-revalidation check.
func matchesETag(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "" {
		return false
	}
	if strings.TrimSpace(ifNoneMatch) == "*" {
		return true
	}
	want := strings.TrimPrefix(etag, "W/")
	for _, part := range strings.Split(ifNoneMatch, ",") {
		if strings.TrimPrefix(strings.TrimSpace(part), "W/") == want {
			return true
		}
	}
	return false
}
