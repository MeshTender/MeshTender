package web

import (
	"net/http"
	"strings"

	"github.com/MeshTender/MeshTender/internal/config"
)

// Top-nav sections (base.html's "topnav"), named by navSection.
const (
	NavDashboard     = "dashboard"
	NavNodes         = "nodes"
	NavOrganizations = "organizations"
	NavDocs          = "docs"
	NavAdmin         = "admin"
)

// navSection is the top-nav section a request belongs to, from its host and
// path; "" when it's in none (account pages, sign-in, the landing page).
func navSection(cfg *config.Config, r *http.Request) string {
	path := r.URL.Path
	under := func(prefix string) bool { return path == prefix || strings.HasPrefix(path, prefix+"/") }
	switch host := HostWithoutPort(r.Host); {
	case strings.EqualFold(host, cfg.AuthHost):
		return ""
	case strings.EqualFold(host, cfg.RootHost):
		switch {
		case under("/orgs"):
			return NavOrganizations
		case under("/docs"):
			return NavDocs
		}
		return ""
	}
	switch {
	case path == "/":
		return NavDashboard
	// Companions, repeaters and observers are all nodes: their own pages sit
	// under the Nodes list.
	case under("/nodes"), under("/companions"), under("/repeaters"), under("/observers"):
		return NavNodes
	case under("/orgs"):
		return NavOrganizations
	case under("/admin"):
		return NavAdmin
	}
	return ""
}
