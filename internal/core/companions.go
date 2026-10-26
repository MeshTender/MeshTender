package core

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/ingest"
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
	})
}

// pageNewCompanion is the register-a-companion form.
func (s *Handlers) pageNewCompanion(w http.ResponseWriter, r *http.Request) {
	companions, err := s.Store.ListCompanionsForUser(r.Context(), s.Auth.CurrentUserID(r.Context()))
	if err != nil {
		s.ServerError(w, r, "Couldn't load your companions.", err)
		return
	}
	s.Render(w, r, "companion_new.html", map[string]any{
		"AtLimit":  len(companions) >= store.MaxCompanionsPerUser,
		"Max":      store.MaxCompanionsPerUser,
		"TTLHours": int(store.UnconfirmedCompanionTTL.Hours()),
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
	addErr := func(msg string) { web.RedirectErr(w, r, "/companions/new", msg) }

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
	if err == nil && r.FormValue("public") == "1" {
		err = s.Store.SetCompanionPublic(r.Context(), uid, c.PublicID, true)
	}
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

// handleCompanionVisibility shows or hides the owner's companion on their
// public profile.
func (s *Handlers) handleCompanionVisibility(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id := chi.URLParam(r, "id")
	public := r.FormValue("public") == "1"
	err := s.Store.SetCompanionPublic(r.Context(), uid, id, public)
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't save the companion.", err)
		return
	}
	msg := "Hidden from your public profile."
	if public {
		msg = "Shown on your public profile."
	}
	web.RedirectOK(w, r, "/companions/"+id, msg)
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
	// Check-ins are counts (observers, spread), never paths: nothing here says
	// which repeaters carried the member's adverts, so org-mates can see it too.
	before, _ := web.DecodeCursor[store.CheckinCursor](r.URL.Query().Get("before"))
	var cursor *store.CheckinCursor
	if !before.FirstHeardAt.IsZero() {
		cursor = &before
	}
	checkins, hasMore, err := s.Store.ListCompanionCheckinsPage(ctx, c.ID, cursor)
	if err != nil {
		s.ServerError(w, r, "Couldn't load check-ins.", err)
		return
	}
	data := map[string]any{
		"Companion": c,
		"IsOwner":   c.OwnerID == uid,
		"Checkins":  checkins,
		"TTLHours":  int(store.UnconfirmedCompanionTTL.Hours()),
		"MaxSkew":   int(ingest.MaxAdvertSkew.Minutes()),
	}
	if hasMore && len(checkins) > 0 {
		last := checkins[len(checkins)-1]
		data["NextBefore"] = web.EncodeCursor(store.CheckinCursor{FirstHeardAt: last.FirstHeardAt, ID: last.ID})
	}
	s.Render(w, r, "companion.html", data)
}
