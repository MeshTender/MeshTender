package web

import (
	"net/http"
)

// pageCommandLog shows the command audit log for a repeater (owner only).
func (s *Server) pageCommandLog(w http.ResponseWriter, r *http.Request) {
	owner := s.auth.CurrentUserID(r.Context())
	id, ok := parseID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	rep, err := s.store.GetRepeaterOwned(r.Context(), owner, id)
	if err != nil {
		http.NotFound(w, r) // owner-only
		return
	}
	sessions, err := s.store.ListCommandLogSessions(r.Context(), id, 500)
	if err != nil {
		http.Error(w, "could not load log", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "command_log.html", map[string]any{
		"Repeater": rep,
		"Sessions": sessions,
	})
}
