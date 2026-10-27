package web

import (
	"strings"

	"github.com/MeshTender/MeshTender/internal/store"
)

// RepeaterNav is the data the shared repeater-header, repeater-tabs and
// repeater-settings-nav partials expect: the repeater's public id, name, owner
// display name, whether it has a public page (the header links to it), whether
// the viewer owns it (gates the Settings tab), and the active page: "overview" |
// "configure" | "maintenance", or "settings-<section>" for a Settings page
// ("settings-details", "settings-docs", "settings-people",
// "settings-organizations", "settings-ownership"). Tab is the top-level tab that
// page lives under.
func RepeaterNav(rep *store.Repeater, isOwner bool, active string) map[string]any {
	tab := active
	if strings.HasPrefix(active, "settings") {
		tab = "settings"
	}
	return map[string]any{
		"PublicID":  rep.PublicID,
		"Name":      rep.Name,
		"OwnerName": rep.OwnerName(),
		"Public":    rep.ExposePublicPage,
		"IsOwner":   isOwner,
		"Active":    active,
		"Tab":       tab,
	}
}
