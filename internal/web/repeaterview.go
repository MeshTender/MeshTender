package web

import "strings"

// RepeaterNav is the data the shared repeater-header, repeater-tabs and
// repeater-settings-nav partials expect: the repeater's public id, name, and
// owner display name, whether the viewer owns it (gates the owner-only tabs),
// and the active page: "overview" | "configure" | "maintenance", or
// "settings-<section>" for a Settings page ("settings-details", "settings-docs",
// "settings-people", "settings-organizations", "settings-ownership"). Tab is the top-level tab that page
// lives under.
func RepeaterNav(publicID, name, ownerName string, isOwner bool, active string) map[string]any {
	tab := active
	if strings.HasPrefix(active, "settings") {
		tab = "settings"
	}
	return map[string]any{
		"PublicID":  publicID,
		"Name":      name,
		"OwnerName": ownerName,
		"IsOwner":   isOwner,
		"Active":    active,
		"Tab":       tab,
	}
}
