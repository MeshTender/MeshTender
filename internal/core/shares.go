package core

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// pageShare renders the sharing page for a repeater the user owns: the current
// share link (if any) and the list of people who have accepted.
func (s *Handlers) pageShare(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := s.requireRepeaterOwned(w, r)
	if !ok {
		return
	}
	shares, err := s.Store.ListShares(r.Context(), id)
	if err != nil {
		s.ServerError(w, r, "could not load shares", err)
		return
	}
	invites, err := s.Store.ListInvites(r.Context(), id)
	if err != nil {
		s.ServerError(w, r, "could not load links", err)
		return
	}
	// Organizations section: every org the owner belongs to, with whether this
	// repeater participates (the default) or has been opted out.
	orgs, err := s.Store.ListRepeaterOrgMemberships(r.Context(), id)
	if err != nil {
		s.ServerError(w, r, "could not load organizations", err)
		return
	}
	s.Render(w, r, "share.html", map[string]any{
		"Repeater": rep,
		"Nav":      web.RepeaterNav(rep.PublicID, rep.Name, rep.OwnerName(), true, "sharing"),
		"Shares":   shares,
		"Invites":  invites,
		"Orgs":     orgs,
		"BaseURL":  s.absoluteURL(r, ""),
		"Error":    r.URL.Query().Get("error"),
	})
}

// handleCreateLink mints a new single-use share link with a description (owner only).
func (s *Handlers) handleCreateLink(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOwned(w, r)
	if !ok {
		return
	}
	description := web.Clip(strings.TrimSpace(r.FormValue("description")), 100)
	if _, err := s.Store.CreateInvite(r.Context(), id, description); err != nil {
		shareErr(w, r, "Could not create share link.")
		return
	}
	http.Redirect(w, r, sharePath(repeaterParam(r)), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// handleDeleteInvite revokes (or clears) a single share link by id (owner only).
func (s *Handlers) handleDeleteInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOwned(w, r)
	if !ok {
		return
	}
	inviteID, err := strconv.ParseInt(r.FormValue("invite_id"), 10, 64)
	if err != nil {
		shareErr(w, r, "Invalid link.")
		return
	}
	if err := s.Store.DeleteInvite(r.Context(), id, inviteID); err != nil {
		shareErr(w, r, "Could not remove link.")
		return
	}
	http.Redirect(w, r, sharePath(repeaterParam(r)), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// handleSetShareSteward flags or unflags a shared user as a steward (owner only).
func (s *Handlers) handleSetShareSteward(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOwned(w, r)
	if !ok {
		return
	}
	targetID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		shareErr(w, r, "Invalid user.")
		return
	}
	if shared, err := s.Store.IsShared(r.Context(), id, targetID); err != nil || !shared {
		http.NotFound(w, r)
		return
	}
	if err := s.Store.SetShareSteward(r.Context(), id, targetID, r.FormValue("steward") == "1"); err != nil {
		shareErr(w, r, "Could not update steward.")
		return
	}
	http.Redirect(w, r, sharePath(repeaterParam(r)), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// handleUnshare revokes a user's access (owner only).
func (s *Handlers) handleUnshare(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOwned(w, r)
	if !ok {
		return
	}
	targetID, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil {
		shareErr(w, r, "Invalid user.")
		return
	}
	if err := s.Store.RemoveShare(r.Context(), id, targetID); err != nil {
		shareErr(w, r, "Could not revoke access.")
		return
	}
	http.Redirect(w, r, sharePath(repeaterParam(r)), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// --- invite accept flow ---

// pageInvite shows the accept page for a share link. It handles logged-out
// users (offer login/register), the owner, already-shared users, and the
// confirm-and-accept case.
func (s *Handlers) pageInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	uid := s.Auth.CurrentUserID(r.Context())

	// queryUserID drives the Shared flag; 0 when logged out is fine.
	rep, err := s.Store.RepeaterByInviteToken(r.Context(), uid, token)
	if errors.Is(err, store.ErrNotFound) {
		s.Render(w, r, "invite.html", map[string]any{"State": "invalid"})
		return
	}
	if err != nil {
		s.ServerError(w, r, "could not load invite", err)
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
		shared, err := s.Store.IsShared(r.Context(), rep.ID, uid)
		if err != nil {
			s.ServerError(w, r, "could not check access", err)
			return
		}
		if shared {
			data["State"] = "already"
		} else {
			data["State"] = "confirm"
		}
	}
	s.Render(w, r, "invite.html", data)
}

// handleAcceptInvite grants the logged-in user access via a single-use share
// link, consuming the link atomically.
func (s *Handlers) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	uid := s.Auth.CurrentUserID(r.Context())

	rep, err := s.Store.RepeaterByInviteToken(r.Context(), uid, token)
	if errors.Is(err, store.ErrNotFound) {
		s.Render(w, r, "invite.html", map[string]any{"State": "invalid"})
		return
	}
	if err != nil {
		s.ServerError(w, r, "could not load invite", err)
		return
	}
	// Don't consume a single-use link for the owner or someone who already has
	// access — those are no-ops.
	if rep.OwnerID == uid {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if shared, err := s.Store.IsShared(r.Context(), rep.ID, uid); err == nil && shared {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Consume the link, grant the share, and seed default commands atomically, so a
	// failure can never spend the link without granting access. The used_at guard
	// inside makes it the single-use gate against concurrent accepts.
	if _, err := s.Store.AcceptInvite(r.Context(), token, uid); errors.Is(err, store.ErrNotFound) {
		s.Render(w, r, "invite.html", map[string]any{"State": "invalid"})
		return
	} else if err != nil {
		s.ServerError(w, r, "could not accept invite", err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// commandGroup is a feature group of catalog commands for the share-commands UI.
type commandGroup = categoryGroup[commandChoice]

type commandChoice struct {
	ID          int64
	Template    string
	Args        string
	Description string
	Risky       bool
	Checked     bool
	// MemberAllowed is true when regular members (and, implicitly, admins) may run
	// the command; false means it's restricted to org admins. Surfaced as the
	// "Access" column on the org Limit-commands page.
	MemberAllowed bool
}

// groupCommands buckets the catalog by feature, marking those whose id is in
// `checked`.
func groupCommands(catalog []*store.Command, checked map[int64]bool) []commandGroup {
	return groupByFeature(catalog, func(c *store.Command) commandChoice {
		return commandChoice{
			ID: c.ID, Template: c.Template, Args: c.Args, Description: c.Description,
			Risky: c.Risky, Checked: checked[c.ID], MemberAllowed: c.OrgMemberAllowed,
		}
	})
}

// pageShareCommands lets a repeater owner choose which commands a shared user may run.
func (s *Handlers) pageShareCommands(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := s.requireRepeaterOwned(w, r)
	if !ok {
		return
	}
	targetID, terr := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if terr != nil {
		http.NotFound(w, r)
		return
	}
	if shared, err := s.Store.IsShared(r.Context(), id, targetID); err != nil || !shared {
		http.NotFound(w, r)
		return
	}
	// A steward already has every command; per-command limits don't apply to them.
	if steward, err := s.Store.IsSteward(r.Context(), id, targetID); err == nil && steward {
		http.Redirect(w, r, sharePath(repeaterParam(r)), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
		return
	}
	target, err := s.Store.GetUserByID(r.Context(), targetID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	catalog, err := s.Store.ListCommands(r.Context())
	if err != nil {
		s.ServerError(w, r, "could not load commands", err)
		return
	}
	ids, _ := s.Store.ListShareCommandIDs(r.Context(), id, targetID)
	checked := make(map[int64]bool, len(ids))
	for _, cid := range ids {
		checked[cid] = true
	}
	s.Render(w, r, "share_commands.html", map[string]any{
		"Repeater": rep,
		"Target":   target,
		"Groups":   groupCommands(catalog, checked),
	})
}

// handleSetShareCommands saves the chosen command set for a shared user.
func (s *Handlers) handleSetShareCommands(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOwned(w, r)
	if !ok {
		return
	}
	targetID, terr := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if terr != nil {
		http.NotFound(w, r)
		return
	}
	if shared, err := s.Store.IsShared(r.Context(), id, targetID); err != nil || !shared {
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
	if err := s.Store.SetShareCommands(r.Context(), id, targetID, cmdIDs); err != nil {
		s.ServerError(w, r, "could not save commands", err)
		return
	}
	http.Redirect(w, r, sharePath(repeaterParam(r)), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// --- helpers ---

// requireRepeaterAccess resolves the {id} param and loads the repeater if the
// current user owns it or has shared/org access, writing the response (404 for an
// unknown id or no access, 500 on an unexpected error) and returning ok=false
// otherwise. It is the control-action gate for pages any authorized user can see.
func (s *Handlers) requireRepeaterAccess(w http.ResponseWriter, r *http.Request) (*store.Repeater, int64, bool) {
	return s.loadRepeater(w, r, s.Store.GetRepeaterForUser)
}

// requireRepeaterOwned is the owner-only variant of requireRepeaterAccess.
func (s *Handlers) requireRepeaterOwned(w http.ResponseWriter, r *http.Request) (*store.Repeater, int64, bool) {
	return s.loadRepeater(w, r, s.Store.GetRepeaterOwned)
}

// loadRepeater resolves {id} and loads the repeater via get (an access-scoped
// lookup), mapping ErrNotFound to 404 and other failures to 500.
func (s *Handlers) loadRepeater(w http.ResponseWriter, r *http.Request,
	get func(ctx context.Context, userID, repeaterID int64) (*store.Repeater, error),
) (*store.Repeater, int64, bool) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
		http.NotFound(w, r)
		return nil, 0, false
	}
	rep, err := get(r.Context(), uid, id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return nil, 0, false
	}
	if err != nil {
		s.ServerError(w, r, "could not load repeater", err)
		return nil, 0, false
	}
	return rep, id, true
}

// requireOwned is requireRepeaterOwned for callers that only need the id.
func (s *Handlers) requireOwned(w http.ResponseWriter, r *http.Request) (int64, bool) {
	_, id, ok := s.requireRepeaterOwned(w, r)
	return id, ok
}

// repeaterID resolves the opaque {id} URL param (a public_id) to the internal
// int64 primary key used by the store. Returns ok=false for unknown ids.
func (s *Handlers) repeaterID(r *http.Request) (int64, bool) {
	id, err := s.Store.RepeaterIDByPublicID(r.Context(), chi.URLParam(r, "id"))
	return id, err == nil
}

// repeaterParam returns the raw public_id from the {id} URL param, for building
// redirect URLs without a round-trip to the store.
func repeaterParam(r *http.Request) string { return chi.URLParam(r, "id") }

func sharePath(publicID string) string { return "/repeaters/" + publicID + "/share" }

func shareErr(w http.ResponseWriter, r *http.Request, msg string) {
	web.RedirectErr(w, r, sharePath(repeaterParam(r)), msg)
}

// absoluteURL builds an absolute URL for a path using the request's scheme/host.
func (s *Handlers) absoluteURL(r *http.Request, path string) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host + path
}
