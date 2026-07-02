package web

import (
	"encoding/base64"
	"encoding/json"
)

// EncodeCursor packs a keyset position into an opaque, URL-safe token. Callers
// pass a small named struct with short JSON tags (the wire form is not stable
// API — it only round-trips through the client's ?cursor/?before parameter).
func EncodeCursor[T any](pos T) string {
	b, _ := json.Marshal(pos)
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor reverses EncodeCursor. A missing or malformed token decodes to the
// zero value with ok=false, so a tampered or stale URL just resets paging to the
// first page rather than erroring.
func DecodeCursor[T any](tok string) (pos T, ok bool) {
	if tok == "" {
		return pos, false
	}
	b, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return pos, false
	}
	if json.Unmarshal(b, &pos) != nil {
		var zero T
		return zero, false
	}
	return pos, true
}
