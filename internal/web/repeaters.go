package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/jleight/meshtender/internal/store"
)

// handleAddRepeater registers a new repeater (unconfirmed) for the current user.
func (s *Server) handleAddRepeater(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())

	name := strings.TrimSpace(r.FormValue("name"))
	pubHex := strings.ToLower(strings.TrimSpace(r.FormValue("public_key")))
	if name == "" {
		dashErr(w, r, "Repeater name is required.")
		return
	}
	// Validate the public key is a real 32-byte MeshCore key.
	if _, err := meshcore.NewIdentityFromHex(pubHex); err != nil {
		dashErr(w, r, "Public key must be 64 hex characters (a 32-byte MeshCore key).")
		return
	}

	freq, err1 := strconv.ParseInt(r.FormValue("radio_freq_hz"), 10, 64)
	bw, err2 := strconv.ParseInt(r.FormValue("radio_bw_hz"), 10, 64)
	sf, err3 := strconv.ParseInt(r.FormValue("radio_sf"), 10, 16)
	cr, err4 := strconv.ParseInt(r.FormValue("radio_cr"), 10, 16)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || freq <= 0 || bw <= 0 {
		dashErr(w, r, "Radio parameters must be valid numbers.")
		return
	}

	_, err := s.store.CreateRepeater(r.Context(), &store.Repeater{
		OwnerID:      uid,
		Name:         name,
		PublicKeyHex: pubHex,
		RadioFreqHz:  freq,
		RadioBwHz:    bw,
		RadioSF:      int16(sf),
		RadioCR:      int16(cr),
	})
	if errors.Is(err, store.ErrDuplicate) {
		dashErr(w, r, "You already added a repeater with that public key.")
		return
	}
	if err != nil {
		dashErr(w, r, "Could not add repeater.")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleDeleteRepeater removes a repeater the current user owns.
func (s *Server) handleDeleteRepeater(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		dashErr(w, r, "Invalid repeater.")
		return
	}
	if err := s.store.DeleteRepeaterOwned(r.Context(), uid, id); err != nil {
		dashErr(w, r, "Could not delete repeater (only the owner can).")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func dashErr(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?error="+url.QueryEscape(msg), http.StatusSeeOther)
}
