package core

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// orgContext resolves the {id} repeater (owned) and {orgID} the user belongs to.
func (s *Handlers) orgContext(w http.ResponseWriter, r *http.Request) (*store.Repeater, int64, bool) {
	owner := s.Auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	orgID, oerr := s.Store.OrgIDBySlug(r.Context(), chi.URLParam(r, "orgID"))
	if !ok || oerr != nil {
		http.NotFound(w, r)
		return nil, 0, false
	}
	rep, err := s.Store.GetRepeaterOwned(r.Context(), owner, id)
	if err != nil {
		http.NotFound(w, r)
		return nil, 0, false
	}
	if _, isMember, err := s.Store.OrgRole(r.Context(), orgID, owner); err != nil || !isMember {
		http.NotFound(w, r) // can only contribute to orgs you belong to
		return nil, 0, false
	}
	return rep, orgID, true
}

// pageContribute shows the org's current permission envelope for the owner to
// review before consenting (also used for re-consent).
func (s *Handlers) pageContribute(w http.ResponseWriter, r *http.Request) {
	rep, orgID, ok := s.orgContext(w, r)
	if !ok {
		return
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	versionID, version, err := s.Store.CurrentVersion(r.Context(), orgID)
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
	members := idSet(memberIDs)
	data := map[string]any{
		"Repeater":       rep,
		"Org":            org,
		"Version":        version,
		"MemberFeatures": web.FeatureTableFor(catalog, members),
		// Admins inherit every member command, so their table is member ∪ admin.
		"AdminFeatures": web.FeatureTableFor(catalog, union(idSet(adminIDs), members)),
	}

	// If already contributed and behind the current version, show what changed
	// since the owner last consented.
	cvID, contributed, _ := s.Store.ConsentedVersionID(r.Context(), orgID, rep.ID)
	if contributed && cvID != versionID {
		cAdmin, cMember, err1 := s.Store.VersionCommandIDs(r.Context(), cvID)
		consentedNum, err2 := s.Store.VersionNumber(r.Context(), cvID)
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
			if notes, err := s.Store.VersionNotesSince(r.Context(), orgID, consentedNum); err == nil {
				data["Notes"] = notes
			}
		}
	}

	s.Render(w, r, "contribute.html", data)
}

// pageConsented shows, read-only, the exact commands this repeater is currently
// consented to grant the org — the detail behind "consented to vN" on the
// sharing page. It renders the consented version, which may lag the org's
// current one.
func (s *Handlers) pageConsented(w http.ResponseWriter, r *http.Request) {
	rep, orgID, ok := s.orgContext(w, r)
	if !ok {
		return
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cvID, contributed, err := s.Store.ConsentedVersionID(r.Context(), orgID, rep.ID)
	if err != nil || !contributed {
		http.NotFound(w, r) // not contributed → nothing consented to view
		return
	}
	version, err := s.Store.VersionNumber(r.Context(), cvID)
	if err != nil {
		http.Error(w, "could not load policy", http.StatusInternalServerError)
		return
	}
	adminIDs, memberIDs, err := s.Store.VersionCommandIDs(r.Context(), cvID)
	if err != nil {
		http.Error(w, "could not load policy", http.StatusInternalServerError)
		return
	}
	catalog, err := s.Store.ListCommands(r.Context())
	if err != nil {
		http.Error(w, "could not load commands", http.StatusInternalServerError)
		return
	}
	members := idSet(memberIDs)
	data := map[string]any{
		"Repeater":       rep,
		"Org":            org,
		"Version":        version,
		"MemberFeatures": web.FeatureTableFor(catalog, members),
		"AdminFeatures":  web.FeatureTableFor(catalog, union(idSet(adminIDs), members)),
	}
	// Note (with a re-consent link) when the org has moved past this version.
	if _, current, err := s.Store.CurrentVersion(r.Context(), orgID); err == nil && current > version {
		data["CurrentVersion"] = current
	}
	s.Render(w, r, "consented.html", data)
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
func (s *Handlers) handleContribute(w http.ResponseWriter, r *http.Request) {
	rep, orgID, ok := s.orgContext(w, r)
	if !ok {
		return
	}
	versionID, _, err := s.Store.CurrentVersion(r.Context(), orgID)
	if err != nil {
		http.Error(w, "could not load policy", http.StatusInternalServerError)
		return
	}
	owner := s.Auth.CurrentUserID(r.Context())
	if err := s.Store.ContributeRepeater(r.Context(), orgID, rep.ID, versionID, owner); err != nil {
		http.Error(w, "could not contribute", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/repeaters/"+rep.PublicID+"/share", http.StatusSeeOther)
}

// handleWithdraw removes the repeater from the org.
func (s *Handlers) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	rep, orgID, ok := s.orgContext(w, r)
	if !ok {
		return
	}
	if err := s.Store.WithdrawRepeater(r.Context(), orgID, rep.ID); err != nil {
		http.Error(w, "could not withdraw", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/repeaters/"+rep.PublicID+"/share", http.StatusSeeOther)
}
