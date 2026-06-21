package core

import (
	"net/http"
	"strconv"

	"github.com/jleight/meshtender/internal/store"
)

// permGroup is a category of catalog commands for the org permission editor,
// with per-tier checkbox state.
type permGroup = categoryGroup[permChoice]

type permChoice struct {
	ID            int64
	Template      string
	Args          string
	Risky         bool
	AdminChecked  bool
	MemberChecked bool
}

func groupPermissions(catalog []*store.Command, admin, member map[int64]bool) []permGroup {
	return groupByCategory(catalog, func(c *store.Command) permChoice {
		return permChoice{
			ID: c.ID, Template: c.Template, Args: c.Args, Risky: c.Risky,
			AdminChecked: admin[c.ID], MemberChecked: member[c.ID],
		}
	})
}

func idSet(ids []int64) map[int64]bool {
	m := make(map[int64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func (s *Handlers) pageOrgPermissions(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	org, err := s.Store.GetOrg(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	versionID, version, err := s.Store.CurrentVersion(r.Context(), id)
	if err != nil {
		http.Error(w, "could not load policy", http.StatusInternalServerError)
		return
	}
	adminIDs, memberIDs, err := s.Store.VersionCommandIDs(r.Context(), versionID)
	if err != nil {
		http.Error(w, "could not load policy", http.StatusInternalServerError)
		return
	}
	catalog, err := s.Store.ListCommands(r.Context())
	if err != nil {
		http.Error(w, "could not load commands", http.StatusInternalServerError)
		return
	}
	s.Render(w, r, "org_permissions.html", map[string]any{
		"Org":     org,
		"Version": version,
		"Groups":  groupPermissions(catalog, idSet(adminIDs), idSet(memberIDs)),
	})
}

func (s *Handlers) handleSaveOrgPermissions(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	parse := func(field string) []int64 {
		var ids []int64
		for _, v := range r.Form[field] {
			if cid, err := strconv.ParseInt(v, 10, 64); err == nil {
				ids = append(ids, cid)
			}
		}
		return ids
	}
	uid := s.Auth.CurrentUserID(r.Context())
	note := r.FormValue("note")
	if _, err := s.Store.PublishVersion(r.Context(), id, note, uid, parse("admin"), parse("member")); err != nil {
		http.Error(w, "could not publish", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r), http.StatusSeeOther)
}
