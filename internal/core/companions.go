package core

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/ingest"
	"github.com/MeshTender/MeshTender/internal/observe"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// Companions are a member's own radios, registered so the adverts they send
// count as check-ins. Registration is confirmed by the first advert an org
// observer hears from the key afterwards (see internal/ingest).

// maxCompanionName bounds the display name; MeshCore's own advert names are
// shorter still (31 bytes).
const maxCompanionName = 64

func (s *Handlers) pageCompanions(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	companions, err := s.Store.ListCompanionsForUser(r.Context(), uid)
	if err != nil {
		s.ServerError(w, r, "Couldn't load your companions.", err)
		return
	}
	s.Render(w, r, "companions.html", map[string]any{
		"Companions": companions,
		"Max":        store.MaxCompanionsPerUser,
		"TTLHours":   int(store.UnconfirmedCompanionTTL.Hours()),
		"Error":      r.URL.Query().Get("error"),
	})
}

// normalizeCompanionKey validates a pasted MeshCore public key and returns it in
// the stored form (lowercase hex). The ingester looks companions up by the
// lowercase hex meshcore.Identity.String() produces, so storing any other form
// would make the companion unmatchable.
func normalizeCompanionKey(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	id, err := meshcore.NewIdentityFromHex(key)
	if err != nil {
		return "", false
	}
	return id.String(), true
}

func (s *Handlers) handleAddCompanion(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	addErr := func(msg string) { web.RedirectErr(w, r, "/companions", msg) }

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || utf8.RuneCountInString(name) > maxCompanionName {
		addErr("Give the companion a name (up to 64 characters).")
		return
	}
	key, ok := normalizeCompanionKey(r.FormValue("public_key"))
	if !ok {
		addErr("That isn't a MeshCore public key. Paste the 64 hex characters from the companion app's contact sharing.")
		return
	}
	c, err := s.Store.CreateCompanion(r.Context(), uid, name, key)
	switch {
	case errors.Is(err, store.ErrDuplicate):
		addErr("That companion is already registered. If it's yours and you didn't register it, ask a site admin.")
		return
	case errors.Is(err, store.ErrCompanionLimit):
		addErr("You've reached the limit of companions. Remove one first.")
		return
	case err != nil:
		s.ServerError(w, r, "Couldn't register the companion.", err)
		return
	}
	http.Redirect(w, r, "/companions/"+c.PublicID, http.StatusSeeOther) //nolint:gosec // G710: local path; PublicID is server-generated
}

func (s *Handlers) handleDeleteCompanion(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	err := s.Store.DeleteCompanionOwned(r.Context(), uid, chi.URLParam(r, "id"))
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't remove the companion.", err)
		return
	}
	http.Redirect(w, r, "/companions", http.StatusSeeOther)
}

// sightingView is one sighting with its path resolved to repeater names.
type sightingView struct {
	store.AdvertSighting
	Route string
	Hops  []observe.Hop
}

// routeView is one of a companion's routes with its path resolved.
type routeView struct {
	store.CompanionRoute
	Hops []observe.Hop
}

// routeLabel names how an advert travelled. A direct advert with no path is how
// firmware sends zero-hop; transport routes are region-scoped.
func routeLabel(routeType byte, hops int) string {
	switch routeType {
	case meshcore.RouteTypeFlood:
		return "Flood"
	case meshcore.RouteTypeTransportFlood:
		return "Flood (scoped)"
	case meshcore.RouteTypeDirect, meshcore.RouteTypeTransportDirect:
		if hops == 0 {
			return "Zero-hop"
		}
		return "Direct"
	}
	return "Unknown"
}

func (s *Handlers) pageCompanion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)
	c, err := s.Store.GetCompanionForViewer(ctx, chi.URLParam(r, "id"), uid)
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't load the companion.", err)
		return
	}
	now := time.Now()
	routes, err := s.Store.ListCompanionRoutes(ctx, c.ID, now)
	if err != nil {
		s.ServerError(w, r, "Couldn't load routes.", err)
		return
	}
	before, _ := web.DecodeCursor[store.SightingCursor](r.URL.Query().Get("before"))
	var cursor *store.SightingCursor
	if !before.ReceivedAt.IsZero() {
		cursor = &before
	}
	sightings, hasMore, err := s.Store.ListCompanionSightingsPage(ctx, c.ID, cursor)
	if err != nil {
		s.ServerError(w, r, "Couldn't load sightings.", err)
		return
	}
	keys, err := s.Store.ListRepeaterKeysForViewer(ctx, uid)
	if err != nil {
		s.ServerError(w, r, "Couldn't load repeaters.", err)
		return
	}
	resolver := observe.NewPathResolver(keys)

	rv := make([]routeView, len(routes))
	for i, rt := range routes {
		rv[i] = routeView{CompanionRoute: rt, Hops: resolver.Resolve(rt.Hops())}
	}
	sv := make([]sightingView, len(sightings))
	for i, sg := range sightings {
		hops := sg.Hops()
		sv[i] = sightingView{AdvertSighting: sg, Route: routeLabel(sg.RouteType, len(hops)), Hops: resolver.Resolve(hops)}
	}
	data := map[string]any{
		"Companion": c,
		"IsOwner":   c.OwnerID == uid,
		"Routes":    rv,
		"Sightings": sv,
		"TTLHours":  int(store.UnconfirmedCompanionTTL.Hours()),
		"MaxSkew":   int(ingest.MaxAdvertSkew.Minutes()),
	}
	if hasMore && len(sightings) > 0 {
		last := sightings[len(sightings)-1]
		data["NextBefore"] = web.EncodeCursor(store.SightingCursor{ReceivedAt: last.ReceivedAt, ID: last.ID})
	}
	s.Render(w, r, "companion.html", data)
}
