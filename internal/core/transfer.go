package core

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// Ownership transfer hands a repeater to one of its stewards — the co-maintainers
// the owner already designated. It is the alternative to deleting a node when its
// owner moves on: the site's documentation, history, and public page address stay
// with the repeater instead of going away with the account.

// transferPath is the transfer page for a repeater, by public id.
func transferPath(publicID string) string { return "/repeaters/" + publicID + "/transfer" }

// transferErr bounces back to the transfer page with an error banner, so the
// owner can pick a different steward rather than losing the page.
func transferErr(w http.ResponseWriter, r *http.Request, msg string) {
	web.RedirectErr(w, r, transferPath(repeaterParam(r)), msg)
}

// pageTransferRepeater renders the owner-only recipient picker: which stewards
// can receive this repeater, and what changes when one does. With no stewards it
// explains how to designate one instead of offering an empty form.
//
// htmx gets the modal fragment (the share page's "Transfer ownership" button);
// a direct visit — the delete page links here, and a browser that never ran the
// htmx request lands here too — gets the standalone page. Both render the same
// content blocks, so there is one copy of the wording.
func (s *Handlers) pageTransferRepeater(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := s.requireRepeaterOwned(w, r)
	if !ok {
		return
	}
	stewards, err := s.Store.ListStewards(r.Context(), id)
	if err != nil {
		s.ServerError(w, r, "could not load stewards", err)
		return
	}
	data := map[string]any{
		"Repeater": rep,
		"Nav":      web.RepeaterNav(rep.PublicID, rep.Name, rep.OwnerName(), true, "sharing"),
		"Stewards": stewards,
		"Error":    r.URL.Query().Get("error"),
	}
	if r.Header.Get("HX-Request") != "" {
		data["Layout"] = "transfer-modal"
	}
	s.Render(w, r, "transfer_repeater.html", data)
}

// handleTransferRepeater hands the repeater to the chosen steward. Authorization
// is the store's job — it re-checks ownership and the steward flag inside the
// transaction — so a stale form naming a since-demoted steward is rejected there
// rather than trusted here.
func (s *Handlers) handleTransferRepeater(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.requireOwned(w, r)
	if !ok {
		return
	}
	targetID, perr := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if perr != nil {
		transferErr(w, r, "Choose who should take over this repeater.")
		return
	}
	err := s.Store.TransferRepeaterToSteward(r.Context(), uid, id, targetID)
	switch {
	case errors.Is(err, store.ErrNotSteward):
		transferErr(w, r, "That person isn't a steward of this repeater anymore. Make them a steward again, or choose someone else.")
	case errors.Is(err, store.ErrDuplicate):
		transferErr(w, r, "That person has already registered this repeater under their own account, so it can't be transferred to them. Ask them to remove their copy first.")
	case errors.Is(err, store.ErrNotFound):
		// Someone else already transferred it, or it was deleted, between the page
		// rendering and this submit. The dashboard is the honest place to land.
		dashErr(w, r, "Could not transfer repeater (only the current owner can).")
	case err != nil:
		s.ServerError(w, r, "could not transfer repeater", err)
	default:
		// The outgoing owner keeps access as a steward, so the repeater page still
		// renders for them — the right place to see the handover took effect.
		web.RedirectFlash(w, r, "/repeaters/"+repeaterParam(r), "ok",
			"Ownership transferred. You're now a steward of this repeater.")
	}
}
