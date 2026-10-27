package core

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/MeshTender/MeshTender/internal/config"
	"github.com/MeshTender/MeshTender/internal/web"
)

// A repeater's Settings tab (owner only), laid out like an org's: a rail of
// sections, each its own page under /repeaters/{id}/settings.
//
//	details   — name, radio, visibility (saved by handleEditRepeater)
//	docs      — public documentation and internal notes (handleRepeaterDocs);
//	            everyone with access reads them on the Overview
//	people        — invite links and the people with access (shares.go)
//	organizations — opting out of an org, or limiting what it may run
//	                (org_participation.go)
//	ownership     — transfer to a steward, or delete
const (
	settingsDetails   = "details"
	settingsDocs      = "docs"
	settingsPeople    = "people"
	settingsOrgs      = "organizations"
	settingsOwnership = "ownership"
)

// settingsPath is a Settings section's URL; details is the tab's own.
func settingsPath(publicID, section string) string {
	p := "/repeaters/" + publicID + "/settings"
	if section != settingsDetails {
		p += "/" + section
	}
	return p
}

// pageRepeaterSettings renders one section of a repeater's Settings tab.
func (s *Handlers) pageRepeaterSettings(w http.ResponseWriter, r *http.Request) {
	section := chi.URLParam(r, "section")
	if section == "" {
		section = settingsDetails
	}
	switch section {
	case settingsDetails, settingsDocs, settingsPeople, settingsOrgs, settingsOwnership:
	default:
		s.NotFound(w, r)
		return
	}
	rep, id, ok := s.requireRepeaterOwned(w, r)
	if !ok {
		return
	}
	data := map[string]any{
		"Repeater": rep,
		"Nav":      web.RepeaterNav(rep.PublicID, rep.Name, rep.OwnerName(), true, "settings-"+section),
		"Section":  section,
	}
	switch section {
	case settingsDetails:
		data["Radio"] = newRadioFields(config.RadioDefaults{FreqHz: uint32(rep.RadioFreqHz), BwHz: uint32(rep.RadioBwHz), SF: uint8(rep.RadioSF), CR: uint8(rep.RadioCR)}, true) //nolint:gosec // G115: radio config value is bounded (preset-constrained)
	case settingsPeople:
		if err := s.peopleData(r, id, data); err != nil {
			s.ServerError(w, r, "could not load sharing", err)
			return
		}
	case settingsOrgs:
		orgs, err := s.Store.ListRepeaterOrgMemberships(r.Context(), id)
		if err != nil {
			s.ServerError(w, r, "could not load organizations", err)
			return
		}
		data["Orgs"] = orgs
	case settingsOwnership:
		data["RevokeCommand"] = s.Identity.RevokePermCommand()
	}
	s.Render(w, r, "repeater_settings.html", data)
}

// redirectToSettings sends an old page's URL (bookmarks, links in mail) to the
// Settings section that replaced it.
func (s *Handlers) redirectToSettings(section string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, settingsPath(repeaterParam(r), section), http.StatusMovedPermanently) //nolint:gosec // G710: local path
	}
}
