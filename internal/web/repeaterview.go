package web

// RepeaterNav is the data the shared repeater-header and repeater-tabs partials
// expect: the repeater's public id, name, and owner display name, whether the
// viewer owns it (gates the Manage menu and the owner-only tabs), and which tab
// is active ("overview" | "docs" | "maintenance" | "log" | "sharing").
func RepeaterNav(publicID, name, ownerName string, isOwner bool, active string) map[string]any {
	return map[string]any{
		"PublicID":  publicID,
		"Name":      name,
		"OwnerName": ownerName,
		"IsOwner":   isOwner,
		"Active":    active,
	}
}
