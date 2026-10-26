package marketing

import (
	"errors"
	"html/template"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/auth"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// companionView is a public companion on the profile, with a QR code that adds
// it as a contact in the MeshCore app.
type companionView struct {
	*store.Companion
	QR template.URL // empty if encoding failed; the key still shows
}

// pageUserPublic renders a user's public profile (/u/{username}) for anyone. All
// profile fields are optional; an unfilled profile just shows the display name.
// Links come first (primary contact on top), then the companions the user chose
// to show, each expanding to a scannable QR code.
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
	// Surface the primary contact first (it's how people are meant to reach this
	// person); keep the editor's order otherwise.
	sort.SliceStable(links, func(i, j int) bool {
		return links[i].IsPrimary && !links[j].IsPrimary
	})
	companions, err := s.Store.ListPublicCompanions(r.Context(), u.ID)
	if err != nil {
		s.ServerError(w, r, "could not load profile", err)
		return
	}
	views := make([]companionView, len(companions))
	for i, c := range companions {
		// Name the contact what the companion itself advertises, once heard; until
		// then, what its owner registered it as.
		name := c.AdvertName
		if name == "" {
			name = c.Name
		}
		views[i] = companionView{Companion: c}
		if qr, ok := web.QRDataURI(web.MeshCoreContactURI(name, c.PublicKeyHex, int(meshcore.AdvertTypeChat))); ok {
			views[i].QR = qr
		}
	}
	// Whether there's anything beyond the name to show — drives an empty-state hint.
	hasDetails := u.Bio != "" || u.Location != "" || u.Callsign != "" || len(links) > 0 || len(views) > 0
	s.Render(w, r, "user_public.html", map[string]any{
		"ProfileUser": u,
		"Bio":         u.Bio,
		"Location":    u.Location,
		"Callsign":    u.Callsign,
		"Links":       links,
		"Companions":  views,
		"HasDetails":  hasDetails,
	})
}
