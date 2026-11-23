package core

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// Observers are room servers and repeaters running MeshTender's observer
// firmware, which upload what they hear to an organization's MQTT broker. A
// member adds one to their Nodes list after installing the firmware, or by its
// public key; the list keeps only the key (store.UserObserver), and the page
// shows what the organizations whose brokers it reports to have recorded.

// pageAddMyObserver is "Add an observer": install the firmware over USB, or add
// one that already runs it by its public key. With the firmware catalog
// unreachable the install part says so and the rest of the page still works.
func (s *Handlers) pageAddMyObserver(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var roles []installRole
	if idx, err := s.firmware.Index(ctx); err != nil {
		web.LogError(r, "firmware index unavailable", err)
	} else {
		roles = installRoles(idx)
	}
	observers, err := s.Store.ListObserversForUser(ctx, s.Auth.CurrentUserID(ctx))
	if err != nil {
		s.ServerError(w, r, "Couldn't load your observers.", err)
		return
	}
	s.Render(w, r, "observer_new.html", map[string]any{
		"Roles":   roles,
		"AtLimit": len(observers) >= store.MaxObserversPerUser,
		"Max":     store.MaxObserversPerUser,
	})
}

func (s *Handlers) handleAddMyObserver(w http.ResponseWriter, r *http.Request) {
	addErr := func(msg string) { web.RedirectErr(w, r, "/observers/new", msg) }
	key, ok := normalizePublicKey(r.FormValue("public_key"))
	if !ok {
		addErr("That isn't a MeshCore public key. Run get public.key on the observer and paste the 64 hex characters it shows.")
		return
	}
	o, err := s.Store.CreateUserObserver(r.Context(), s.Auth.CurrentUserID(r.Context()), key)
	switch {
	case errors.Is(err, store.ErrDuplicate):
		addErr("That observer is already in your nodes.")
		return
	case errors.Is(err, store.ErrObserverLimit):
		addErr("You've added the most observers you can. Remove one to add another.")
		return
	case err != nil:
		s.ServerError(w, r, "Couldn't add the observer.", err)
		return
	}
	web.RedirectOK(w, r, "/observers/"+o.PublicID, "Observer added.")
}

// pageMyObserver shows one of the user's observers: its latest status and the
// organizations whose brokers it reports to. Organizations the user isn't in
// are counted, not named.
func (s *Handlers) pageMyObserver(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)
	o, err := s.Store.GetUserObserver(ctx, uid, chi.URLParam(r, "id"))
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't load the observer.", err)
		return
	}
	reports, err := s.Store.ObserverReports(ctx, o.PublicKeyHex, uid)
	if err != nil {
		s.ServerError(w, r, "Couldn't load where the observer reports.", err)
		return
	}
	var mine []store.ObserverReport
	for _, rep := range reports {
		if rep.Member {
			mine = append(mine, rep)
		}
	}
	s.Render(w, r, "observer.html", map[string]any{
		"Observer":  o,
		"Reports":   mine,
		"OtherOrgs": o.OrgCount - len(mine),
	})
}

func (s *Handlers) handleDeleteMyObserver(w http.ResponseWriter, r *http.Request) {
	err := s.Store.DeleteUserObserver(r.Context(), s.Auth.CurrentUserID(r.Context()), chi.URLParam(r, "id"))
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't remove the observer.", err)
		return
	}
	web.RedirectOK(w, r, "/nodes", "Observer removed from your nodes.")
}
