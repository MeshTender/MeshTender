package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/store"
)

// orgContext resolves the {id} repeater (owned) and {orgID} the user belongs to.
func (s *Server) orgContext(w http.ResponseWriter, r *http.Request) (*store.Repeater, int64, bool) {
	owner := s.auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	orgID, oerr := s.store.OrgIDBySlug(r.Context(), chi.URLParam(r, "orgID"))
	if !ok || oerr != nil {
		http.NotFound(w, r)
		return nil, 0, false
	}
	rep, err := s.store.GetRepeaterOwned(r.Context(), owner, id)
	if err != nil {
		http.NotFound(w, r)
		return nil, 0, false
	}
	if _, isMember, err := s.store.OrgRole(r.Context(), orgID, owner); err != nil || !isMember {
		http.NotFound(w, r) // can only contribute to orgs you belong to
		return nil, 0, false
	}
	return rep, orgID, true
}

// pageContribute shows the org's current permission envelope for the owner to
// review before consenting (also used for re-consent).
func (s *Server) pageContribute(w http.ResponseWriter, r *http.Request) {
	rep, orgID, ok := s.orgContext(w, r)
	if !ok {
		return
	}
	org, err := s.store.GetOrg(r.Context(), orgID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	versionID, version, err := s.store.CurrentVersion(r.Context(), orgID)
	if err != nil {
		http.Error(w, "could not load policy", http.StatusInternalServerError)
		return
	}
	adminIDs, memberIDs, err := s.store.VersionCommandIDs(r.Context(), versionID)
	if err != nil {
		http.Error(w, "could not load policy", http.StatusInternalServerError)
		return
	}
	catalog, err := s.store.ListCommands(r.Context())
	if err != nil {
		http.Error(w, "could not load commands", http.StatusInternalServerError)
		return
	}
	// Reuse the per-tier grouping; only commands in at least one tier are shown.
	groups := groupPermissions(catalog, idSet(adminIDs), idSet(memberIDs))
	var envelope []permGroup
	for _, g := range groups {
		var cmds []permChoice
		for _, c := range g.Commands {
			if c.AdminChecked || c.MemberChecked {
				cmds = append(cmds, c)
			}
		}
		if len(cmds) > 0 {
			envelope = append(envelope, permGroup{Name: g.Name, Commands: cmds})
		}
	}

	data := map[string]any{
		"Repeater": rep,
		"Org":      org,
		"Version":  version,
		"Envelope": envelope,
	}

	// If already contributed and behind the current version, show what changed
	// since the owner last consented.
	cvID, contributed, _ := s.store.ConsentedVersionID(r.Context(), orgID, rep.ID)
	if contributed && cvID != versionID {
		cAdmin, cMember, err1 := s.store.VersionCommandIDs(r.Context(), cvID)
		consentedNum, err2 := s.store.VersionNumber(r.Context(), cvID)
		if err1 == nil && err2 == nil {
			tmpl := map[int64]string{}
			for _, c := range catalog {
				tmpl[c.ID] = c.Template
			}
			consented := union(idSet(cAdmin), idSet(cMember))
			current := union(idSet(adminIDs), idSet(memberIDs))
			data["Reconsent"] = true
			data["ConsentedVersion"] = consentedNum
			data["Added"] = templatesFor(current, consented, tmpl)   // newly granted
			data["Removed"] = templatesFor(consented, current, tmpl) // no longer granted
			if notes, err := s.store.VersionNotesSince(r.Context(), orgID, consentedNum); err == nil {
				data["Notes"] = notes
			}
		}
	}

	s.render(w, r, "contribute.html", data)
}

// union returns the set union of two id sets.
func union(a, b map[int64]bool) map[int64]bool {
	out := make(map[int64]bool, len(a)+len(b))
	for id := range a {
		out[id] = true
	}
	for id := range b {
		out[id] = true
	}
	return out
}

// templatesFor returns the command templates for ids in `in` but not in `notIn`.
func templatesFor(in, notIn map[int64]bool, tmpl map[int64]string) []string {
	var out []string
	for id := range in {
		if !notIn[id] {
			if t := tmpl[id]; t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// handleContribute pins the repeater to the org's current permission version.
func (s *Server) handleContribute(w http.ResponseWriter, r *http.Request) {
	rep, orgID, ok := s.orgContext(w, r)
	if !ok {
		return
	}
	versionID, _, err := s.store.CurrentVersion(r.Context(), orgID)
	if err != nil {
		http.Error(w, "could not load policy", http.StatusInternalServerError)
		return
	}
	owner := s.auth.CurrentUserID(r.Context())
	if err := s.store.ContributeRepeater(r.Context(), orgID, rep.ID, versionID, owner); err != nil {
		http.Error(w, "could not contribute", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/repeaters/"+rep.PublicID+"/share", http.StatusSeeOther)
}

// handleWithdraw removes the repeater from the org.
func (s *Server) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	rep, orgID, ok := s.orgContext(w, r)
	if !ok {
		return
	}
	if err := s.store.WithdrawRepeater(r.Context(), orgID, rep.ID); err != nil {
		http.Error(w, "could not withdraw", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/repeaters/"+rep.PublicID+"/share", http.StatusSeeOther)
}
