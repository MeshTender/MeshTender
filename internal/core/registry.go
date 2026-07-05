package core

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jleight/meshtender/internal/web"
)

// This file backs the repeater "registry" tabs: site Documentation (public and
// internal) and Maintenance history. Documentation is owner-edited; maintenance
// entries can be logged by anyone with access (co-stewards), but only the owner
// can delete them.

const (
	maxDocLen         = 20000
	maxMaintNoteLen   = 2000
	maxMaintAuthorLen = 120
)

// pageRepeaterDocs renders the Documentation tab. Anyone with access reads it;
// the owner gets edit forms. The internal section is only shown to people with
// access (which everyone reaching this page has) — never on the public page.
func (s *Handlers) pageRepeaterDocs(w http.ResponseWriter, r *http.Request) {
	rep, _, ok := s.requireRepeaterAccess(w, r)
	if !ok {
		return
	}
	isOwner := !rep.Shared
	s.Render(w, r, "repeater_docs.html", map[string]any{
		"Repeater": rep,
		"Nav":      web.RepeaterNav(rep.PublicID, rep.Name, rep.OwnerName(), isOwner, "docs"),
		"IsOwner":  isOwner,
		"Error":    r.URL.Query().Get("error"),
	})
}

// handleRepeaterDocs saves the public and internal documentation (owner only).
func (s *Handlers) handleRepeaterDocs(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOwned(w, r)
	if !ok {
		return
	}
	docPublic := clip(r.FormValue("doc_public"), maxDocLen)
	docInternal := clip(r.FormValue("doc_internal"), maxDocLen)
	if err := s.Store.UpdateRepeaterDocs(r.Context(), s.Auth.CurrentUserID(r.Context()), id, docPublic, docInternal); err != nil {
		web.RedirectErr(w, r, docsPath(repeaterParam(r)), "Could not save documentation.")
		return
	}
	http.Redirect(w, r, docsPath(repeaterParam(r)), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// pageRepeaterMaintenance renders the Maintenance tab: the history plus, for
// anyone with access, a form to log a new entry.
func (s *Handlers) pageRepeaterMaintenance(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := s.requireRepeaterAccess(w, r)
	if !ok {
		return
	}
	entries, err := s.Store.ListMaintenance(r.Context(), id)
	if err != nil {
		s.ServerError(w, r, "could not load maintenance history", err)
		return
	}
	isOwner := !rep.Shared
	s.Render(w, r, "repeater_maintenance.html", map[string]any{
		"Repeater": rep,
		"Nav":      web.RepeaterNav(rep.PublicID, rep.Name, rep.OwnerName(), isOwner, "maintenance"),
		"IsOwner":  isOwner,
		"Entries":  entries,
		"Today":    time.Now().Format("2006-01-02"),
		"Error":    r.URL.Query().Get("error"),
	})
}

// handleAddMaintenance logs a maintenance entry. Anyone with access may add one;
// the author is recorded for the history.
func (s *Handlers) handleAddMaintenance(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := s.requireRepeaterAccess(w, r)
	if !ok {
		return
	}
	note := strings.TrimSpace(r.FormValue("note"))
	if note == "" {
		web.RedirectErr(w, r, maintPath(repeaterParam(r)), "A note is required.")
		return
	}
	note = clip(note, maxMaintNoteLen)

	// performed_at comes from a <input type=date>; default to today if absent or
	// unparseable so a bad value never blocks logging.
	performed := time.Now()
	if v := r.FormValue("performed_at"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			performed = t
		}
	}

	uid := s.Auth.CurrentUserID(r.Context())
	authorName := ""
	if u, err := s.Store.GetUserByID(r.Context(), uid); err == nil {
		authorName = clip(u.Name(), maxMaintAuthorLen)
	}
	if authorName == "" {
		authorName = rep.OwnerName()
	}

	if err := s.Store.AddMaintenanceEntry(r.Context(), id, uid, authorName, note, performed); err != nil {
		web.RedirectErr(w, r, maintPath(repeaterParam(r)), "Could not log maintenance entry.")
		return
	}
	http.Redirect(w, r, maintPath(repeaterParam(r)), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// handleDeleteMaintenance removes a maintenance entry (owner only).
func (s *Handlers) handleDeleteMaintenance(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOwned(w, r)
	if !ok {
		return
	}
	entryID, err := strconv.ParseInt(r.FormValue("entry_id"), 10, 64)
	if err != nil {
		web.RedirectErr(w, r, maintPath(repeaterParam(r)), "Invalid entry.")
		return
	}
	if err := s.Store.DeleteMaintenanceEntry(r.Context(), id, entryID); err != nil {
		web.RedirectErr(w, r, maintPath(repeaterParam(r)), "Could not delete entry.")
		return
	}
	http.Redirect(w, r, maintPath(repeaterParam(r)), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

func docsPath(publicID string) string  { return "/repeaters/" + publicID + "/docs" }
func maintPath(publicID string) string { return "/repeaters/" + publicID + "/maintenance" }

// clip trims a string to at most n bytes, backing off to a valid UTF-8 boundary
// if the cut landed mid-rune. Guards stored text against oversized form input.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
