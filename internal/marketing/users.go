package marketing

import (
	"errors"
	"html/template"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// linkView is one row in the public profile's links list. It embeds the stored
// link (so .Platform/.Display/.Href/.IsPrimary/.URL are available) and adds a QR
// code for MeshCore keys, which render as a collapsible row rather than a link.
type linkView struct {
	store.UserLink
	QR template.URL // non-empty only for MeshCore keys
}

// pageUserPublic renders a user's public profile (/u/{username}) for anyone. All
// profile fields are optional; an unfilled profile just shows the display name.
// Every link renders in one list; a MeshCore key expands to a scannable QR code.
func (s *Handlers) pageUserPublic(w http.ResponseWriter, r *http.Request) {
	username := auth.NormalizeUsername(chi.URLParam(r, "username"))
	u, err := s.Store.GetUserByUsername(r.Context(), username)
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	}
	if err != nil {
		s.ServerError(w, r, "could not load profile", err)
		return
	}
	links, err := s.Store.ListUserLinks(r.Context(), u.ID)
	if err != nil {
		s.ServerError(w, r, "could not load profile", err)
		return
	}
	views := make([]linkView, 0, len(links))
	for _, l := range links {
		lv := linkView{UserLink: l}
		if l.IsMeshCore() {
			if qr, ok := web.QRDataURI(web.MeshCoreContactURI(u.Name(), l.URL, int(meshcore.AdvertTypeChat))); ok {
				lv.QR = qr
			}
		}
		views = append(views, lv)
	}
	// Surface the primary contact first (it's how people are meant to reach this
	// person); keep the editor's order otherwise. MeshCore keys are never primary.
	sort.SliceStable(views, func(i, j int) bool {
		return views[i].IsPrimary && !views[j].IsPrimary
	})
	// Whether there's anything beyond the name to show — drives an empty-state hint.
	hasDetails := u.Bio != "" || u.Location != "" || u.Callsign != "" || len(views) > 0
	s.Render(w, r, "user_public.html", map[string]any{
		"ProfileUser": u,
		"Bio":         u.Bio,
		"Location":    u.Location,
		"Callsign":    u.Callsign,
		"Links":       views,
		"HasDetails":  hasDetails,
	})
}
