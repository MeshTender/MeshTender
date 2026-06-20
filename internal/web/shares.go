package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/store"
)

// pageShare renders the sharing page for a repeater the user owns: the current
// share link (if any) and the list of people who have accepted.
func (s *Server) pageShare(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	rep, err := s.store.GetRepeaterOwned(r.Context(), uid, id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r) // not owner (or doesn't exist)
		return
	}
	if err != nil {
		http.Error(w, "could not load repeater", http.StatusInternalServerError)
		return
	}
	shares, err := s.store.ListShares(r.Context(), id)
	if err != nil {
		http.Error(w, "could not load shares", http.StatusInternalServerError)
		return
	}
	invites, err := s.store.ListInvites(r.Context(), id)
	if err != nil {
		http.Error(w, "could not load links", http.StatusInternalServerError)
		return
	}
	// Organizations section: orgs this repeater is contributed to, plus orgs the
	// owner belongs to but hasn't contributed it to yet.
	contributed, err := s.store.ListRepeaterOrgs(r.Context(), id)
	if err != nil {
		http.Error(w, "could not load orgs", http.StatusInternalServerError)
		return
	}
	memberships, err := s.store.ListOrgsForUser(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load memberships", http.StatusInternalServerError)
		return
	}
	in := map[int64]bool{}
	for _, c := range contributed {
		in[c.OrgID] = true
	}
	var available []*store.Org
	for _, m := range memberships {
		if !in[m.Org.ID] {
			available = append(available, m.Org)
		}
	}
	s.render(w, r, "share.html", map[string]any{
		"Repeater":    rep,
		"Shares":      shares,
		"Invites":     invites,
		"Contributed": contributed,
		"Available":   available,
		"BaseURL":     s.absoluteURL(r, ""),
		"Error":       r.URL.Query().Get("error"),
	})
}

// handleCreateLink mints a new single-use share link with a description (owner only).
func (s *Server) handleCreateLink(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOwned(w, r)
	if !ok {
		return
	}
	description := strings.TrimSpace(r.FormValue("description"))
	if len(description) > 100 {
		description = description[:100]
	}
	if _, err := s.store.CreateInvite(r.Context(), id, description); err != nil {
		shareErr(w, r, "Could not create share link.")
		return
	}
	http.Redirect(w, r, sharePath(repeaterParam(r)), http.StatusSeeOther)
}

// handleDeleteInvite revokes (or clears) a single share link by id (owner only).
func (s *Server) handleDeleteInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOwned(w, r)
	if !ok {
		return
	}
	inviteID, err := strconv.ParseInt(r.FormValue("invite_id"), 10, 64)
	if err != nil {
		shareErr(w, r, "Invalid link.")
		return
	}
	if err := s.store.DeleteInvite(r.Context(), id, inviteID); err != nil {
		shareErr(w, r, "Could not remove link.")
		return
	}
	http.Redirect(w, r, sharePath(repeaterParam(r)), http.StatusSeeOther)
}

// handleUnshare revokes a user's access (owner only).
func (s *Server) handleUnshare(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOwned(w, r)
	if !ok {
		return
	}
	targetID, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil {
		shareErr(w, r, "Invalid user.")
		return
	}
	if err := s.store.RemoveShare(r.Context(), id, targetID); err != nil {
		shareErr(w, r, "Could not revoke access.")
		return
	}
	http.Redirect(w, r, sharePath(repeaterParam(r)), http.StatusSeeOther)
}

// --- invite accept flow ---

// pageInvite shows the accept page for a share link. It handles logged-out
// users (offer login/register), the owner, already-shared users, and the
// confirm-and-accept case.
func (s *Server) pageInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	uid := s.auth.CurrentUserID(r.Context())

	// queryUserID drives the Shared flag; 0 when logged out is fine.
	rep, err := s.store.RepeaterByInviteToken(r.Context(), uid, token)
	if errors.Is(err, store.ErrNotFound) {
		s.render(w, r, "invite.html", map[string]any{"State": "invalid"})
		return
	}
	if err != nil {
		http.Error(w, "could not load invite", http.StatusInternalServerError)
		return
	}

	data := map[string]any{"Repeater": rep, "Token": token}
	switch {
	case uid == 0:
		data["State"] = "auth_required"
		data["Next"] = "/invite/" + token
	case rep.OwnerID == uid:
		data["State"] = "owner"
	default:
		shared, err := s.store.IsShared(r.Context(), rep.ID, uid)
		if err != nil {
			http.Error(w, "could not check access", http.StatusInternalServerError)
			return
		}
		if shared {
			data["State"] = "already"
		} else {
			data["State"] = "confirm"
		}
	}
	s.render(w, r, "invite.html", data)
}

// handleAcceptInvite grants the logged-in user access via a single-use share
// link, consuming the link atomically.
func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	uid := s.auth.CurrentUserID(r.Context())

	rep, err := s.store.RepeaterByInviteToken(r.Context(), uid, token)
	if errors.Is(err, store.ErrNotFound) {
		s.render(w, r, "invite.html", map[string]any{"State": "invalid"})
		return
	}
	if err != nil {
		http.Error(w, "could not load invite", http.StatusInternalServerError)
		return
	}
	// Don't consume a single-use link for the owner or someone who already has
	// access — those are no-ops.
	if rep.OwnerID == uid {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if shared, err := s.store.IsShared(r.Context(), rep.ID, uid); err == nil && shared {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Atomically consume the link (single-use guard against concurrent accepts).
	if _, err := s.store.ConsumeInvite(r.Context(), token, uid); errors.Is(err, store.ErrNotFound) {
		s.render(w, r, "invite.html", map[string]any{"State": "invalid"})
		return
	} else if err != nil {
		http.Error(w, "could not accept invite", http.StatusInternalServerError)
		return
	}
	added, err := s.store.AddShare(r.Context(), rep.ID, uid)
	if err != nil {
		http.Error(w, "could not accept invite", http.StatusInternalServerError)
		return
	}
	if added {
		// Seed the new share with the default command set; owner can adjust.
		_ = s.store.SeedShareCommands(r.Context(), rep.ID, uid)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// commandGroup is a category of catalog commands for the share-commands UI.
type commandGroup struct {
	Name     string
	Commands []commandChoice
}

type commandChoice struct {
	ID       int64
	Template string
	Args     string
	Risky    bool
	Checked  bool
}

// groupCommands buckets the catalog by category, marking those whose id is in
// `checked`.
func groupCommands(catalog []*store.Command, checked map[int64]bool) []commandGroup {
	var groups []commandGroup
	idx := map[string]int{}
	for _, c := range catalog {
		gi, ok := idx[c.Category]
		if !ok {
			gi = len(groups)
			idx[c.Category] = gi
			groups = append(groups, commandGroup{Name: c.Category})
		}
		groups[gi].Commands = append(groups[gi].Commands, commandChoice{
			ID: c.ID, Template: c.Template, Args: c.Args, Risky: c.Risky, Checked: checked[c.ID],
		})
	}
	return groups
}

// pageShareCommands lets a repeater owner choose which commands a shared user may run.
func (s *Server) pageShareCommands(w http.ResponseWriter, r *http.Request) {
	owner := s.auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	targetID, terr := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if !ok || terr != nil {
		http.NotFound(w, r)
		return
	}
	rep, err := s.store.GetRepeaterOwned(r.Context(), owner, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if shared, err := s.store.IsShared(r.Context(), id, targetID); err != nil || !shared {
		http.NotFound(w, r)
		return
	}
	target, err := s.store.GetUserByID(r.Context(), targetID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	catalog, err := s.store.ListCommands(r.Context())
	if err != nil {
		http.Error(w, "could not load commands", http.StatusInternalServerError)
		return
	}
	ids, _ := s.store.ListShareCommandIDs(r.Context(), id, targetID)
	checked := make(map[int64]bool, len(ids))
	for _, cid := range ids {
		checked[cid] = true
	}
	s.render(w, r, "share_commands.html", map[string]any{
		"Repeater": rep,
		"Target":   target,
		"Groups":   groupCommands(catalog, checked),
	})
}

// handleSetShareCommands saves the chosen command set for a shared user.
func (s *Server) handleSetShareCommands(w http.ResponseWriter, r *http.Request) {
	owner := s.auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	targetID, terr := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if !ok || terr != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.store.GetRepeaterOwned(r.Context(), owner, id); err != nil {
		http.NotFound(w, r)
		return
	}
	if shared, err := s.store.IsShared(r.Context(), id, targetID); err != nil || !shared {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	var cmdIDs []int64
	for _, v := range r.Form["cmd"] {
		if cid, err := strconv.ParseInt(v, 10, 64); err == nil {
			cmdIDs = append(cmdIDs, cid)
		}
	}
	if err := s.store.SetShareCommands(r.Context(), id, targetID, cmdIDs); err != nil {
		http.Error(w, "could not save commands", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, sharePath(repeaterParam(r)), http.StatusSeeOther)
}

// --- helpers ---

// requireOwned resolves the {id} param and verifies the current user owns the
// repeater, writing a 404 and returning ok=false otherwise.
func (s *Server) requireOwned(w http.ResponseWriter, r *http.Request) (int64, bool) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
		http.NotFound(w, r)
		return 0, false
	}
	if _, err := s.store.GetRepeaterOwned(r.Context(), uid, id); err != nil {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

// repeaterID resolves the opaque {id} URL param (a public_id) to the internal
// int64 primary key used by the store. Returns ok=false for unknown ids.
func (s *Server) repeaterID(r *http.Request) (int64, bool) {
	id, err := s.store.RepeaterIDByPublicID(r.Context(), chi.URLParam(r, "id"))
	return id, err == nil
}

// repeaterParam returns the raw public_id from the {id} URL param, for building
// redirect URLs without a round-trip to the store.
func repeaterParam(r *http.Request) string { return chi.URLParam(r, "id") }

func sharePath(publicID string) string { return "/repeaters/" + publicID + "/share" }

func shareErr(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, sharePath(repeaterParam(r))+"?error="+url.QueryEscape(msg), http.StatusSeeOther)
}

// absoluteURL builds an absolute URL for a path using the request's scheme/host.
func (s *Server) absoluteURL(r *http.Request, path string) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host + path
}
