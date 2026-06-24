package core

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// pageCommandLog shows the command audit log for a repeater (owner only),
// keyset-paginated by console session via an opaque ?before token.
func (s *Handlers) pageCommandLog(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := s.requireRepeaterOwned(w, r)
	if !ok {
		return
	}
	sessions, hasMore, err := s.Store.ListCommandLogSessionsPage(r.Context(), id, decodeLogCursor(r.URL.Query().Get("before")))
	if err != nil {
		http.Error(w, "could not load log", http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"Repeater": rep,
		"Nav":      web.RepeaterNav(rep.PublicID, rep.Name, rep.OwnerName(), true, "log"),
		"Sessions": sessions,
	}
	if hasMore && len(sessions) > 0 {
		last := sessions[len(sessions)-1]
		data["NextBefore"] = encodeLogCursor(last.StartedAt, last.ID)
	}
	s.Render(w, r, "command_log.html", data)
}

// logCursor is the wire form of a command-log keyset position.
type logCursor struct {
	StartedAt time.Time `json:"t"`
	ID        int64     `json:"i"`
}

// encodeLogCursor packs a session position into an opaque, URL-safe token.
func encodeLogCursor(startedAt time.Time, id int64) string {
	b, _ := json.Marshal(logCursor{StartedAt: startedAt, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeLogCursor reverses encodeLogCursor. A missing or malformed token decodes
// to nil — the first page — so a tampered URL just resets paging.
func decodeLogCursor(tok string) *store.CommandLogCursor {
	if tok == "" {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return nil
	}
	var c logCursor
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	return &store.CommandLogCursor{StartedAt: c.StartedAt, ID: c.ID}
}
