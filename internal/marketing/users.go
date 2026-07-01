package marketing

import (
	"errors"
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// meshKeyView is a MeshCore public key rendered for the public profile: the
// label, the key text, and a QR code that adds the person as a MeshCore contact.
type meshKeyView struct {
	Label string
	Key   string
	QR    template.URL
}

// pageUserPublic renders a user's public profile (/u/{username}) for anyone. All
// profile fields are optional; an unfilled profile just shows the display name.
// MeshCore-key links render as scannable QR codes; other links render as buttons.
func (s *Handlers) pageUserPublic(w http.ResponseWriter, r *http.Request) {
	username := auth.NormalizeUsername(chi.URLParam(r, "username"))
	u, err := s.Store.GetUserByUsername(r.Context(), username)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "could not load profile", http.StatusInternalServerError)
		return
	}
	links, err := s.Store.ListUserLinks(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "could not load profile", http.StatusInternalServerError)
		return
	}
	// Ordinary links render as buttons; MeshCore keys render as QR codes.
	var webLinks []store.UserLink
	var meshKeys []meshKeyView
	for _, l := range links {
		if l.IsMeshCore() {
			mk := meshKeyView{Label: l.Display(), Key: l.URL}
			if qr, ok := web.QRDataURI(web.MeshCoreContactURI(u.Name(), l.URL, int(meshcore.AdvertTypeChat))); ok {
				mk.QR = qr
			}
			meshKeys = append(meshKeys, mk)
			continue
		}
		webLinks = append(webLinks, l)
	}
	// Whether there's anything beyond the name to show — drives an empty-state hint.
	hasDetails := u.Bio != "" || u.Location != "" || u.Callsign != "" || len(webLinks) > 0 || len(meshKeys) > 0
	s.Render(w, r, "user_public.html", map[string]any{
		"ProfileUser": u,
		"Bio":         u.Bio,
		"Location":    u.Location,
		"Callsign":    u.Callsign,
		"Links":       webLinks,
		"MeshKeys":    meshKeys,
		"HasDetails":  hasDetails,
	})
}
