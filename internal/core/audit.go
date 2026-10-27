package core

import (
	"time"

	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// logCursor is the wire form of a command-log keyset position.
type logCursor struct {
	StartedAt time.Time `json:"t"`
	ID        int64     `json:"i"`
}

// encodeLogCursor packs a session position into an opaque, URL-safe token.
func encodeLogCursor(startedAt time.Time, id int64) string {
	return web.EncodeCursor(logCursor{StartedAt: startedAt, ID: id})
}

// decodeLogCursor reverses encodeLogCursor. A missing or malformed token decodes
// to nil — the first page — so a tampered URL just resets paging.
func decodeLogCursor(tok string) *store.CommandLogCursor {
	c, ok := web.DecodeCursor[logCursor](tok)
	if !ok {
		return nil
	}
	return &store.CommandLogCursor{StartedAt: c.StartedAt, ID: c.ID}
}
