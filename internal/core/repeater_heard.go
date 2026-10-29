package core

import (
	"context"
	"errors"
	"net/http"

	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// heardView loads what rep has been heard advertising, compared with what's
// registered for it; nil when it hasn't been heard.
func (s *Handlers) heardView(ctx context.Context, rep *store.Repeater) (*web.HeardView, error) {
	o, err := s.Store.GetRepeaterObservation(ctx, rep.PublicKeyHex)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	regions, err := s.Store.ListRepeaterRegionNames(ctx, rep.ID)
	if err != nil {
		return nil, err
	}
	return web.NewHeardView(o, rep.Name, rep.Latitude, rep.Longitude, regions), nil
}

// adoptedObservation loads the observation an "adopt" button acts on; the value
// always comes from it, never the form, so only what the repeater actually
// advertised can be adopted. It writes the response and returns nil when
// there's none.
func (s *Handlers) adoptedObservation(w http.ResponseWriter, r *http.Request, rep *store.Repeater) *store.RepeaterObservation {
	o, err := s.Store.GetRepeaterObservation(r.Context(), rep.PublicKeyHex)
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return nil
	}
	if err != nil {
		s.ServerError(w, r, "could not load what the repeater advertised", err)
		return nil
	}
	return o
}

// The adopt buttons live in Settings › Details (owner only): the Overview
// flags a mismatch, Settings fixes it.

// handleAdoptHeardName renames an owned repeater to the name it advertises.
func (s *Handlers) handleAdoptHeardName(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := s.requireRepeaterOwned(w, r)
	if !ok {
		return
	}
	o := s.adoptedObservation(w, r, rep)
	if o == nil {
		return
	}
	back := settingsPath(rep.PublicID, settingsDetails)
	name, ok := cleanRepeaterName(o.AdvertName)
	if !ok {
		web.RedirectErr(w, r, back, "The advertised name can't be used as the repeater's name: it must be 31 characters or fewer.")
		return
	}
	if err := s.Store.UpdateRepeater(r.Context(), rep.OwnerID, id, name, rep.RadioFreqHz, rep.RadioBwHz,
		rep.RadioSF, rep.RadioCR, rep.ShowOnPublicOrg, rep.ExposePublicPage); err != nil {
		s.ServerError(w, r, "could not rename repeater", err)
		return
	}
	web.RedirectOK(w, r, back, "Renamed to the advertised name.")
}

// handleAdoptHeardLocation sets an owned repeater's location to the one it
// advertises.
func (s *Handlers) handleAdoptHeardLocation(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := s.requireRepeaterOwned(w, r)
	if !ok {
		return
	}
	o := s.adoptedObservation(w, r, rep)
	if o == nil {
		return
	}
	back := settingsPath(rep.PublicID, settingsDetails)
	if o.AdvertLat == nil || o.AdvertLon == nil {
		web.RedirectErr(w, r, back, "The repeater doesn't advertise a location.")
		return
	}
	if err := s.Store.SetRepeaterLocation(r.Context(), id, *o.AdvertLat, *o.AdvertLon); err != nil {
		s.ServerError(w, r, "could not save location", err)
		return
	}
	web.RedirectOK(w, r, back, "Location set to the advertised one.")
}
