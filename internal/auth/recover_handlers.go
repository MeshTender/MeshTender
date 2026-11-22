package auth

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// pageRecover shows the page an admin-issued recovery link opens. It only looks
// the link up: mail scanners open links before people do, and spending it here
// would leave the person with a dead link.
func (s *Handlers) pageRecover(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	u, ok, err := s.Auth.PeekRecoveryToken(r.Context(), token)
	if err != nil {
		s.ServerError(w, r, "could not check recovery link", err)
		return
	}
	data := map[string]any{"Layout": "authbase"}
	if ok {
		data["Token"] = token
		// Named so the person can see which account they're about to get back into
		// before anything happens.
		data["Username"] = u.Username
	} else {
		data["Invalid"] = true
	}
	s.Render(w, r, "recover.html", data)
}
