package core

import (
	"fmt"
	"net/http"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// The CSP violation report view. Reports arrive at web.CSPReportPath on every host
// and are counted into one aggregate table; this page reads it.
//
// The default filter is source=page. Extension noise dominates real-world CSP
// reporting — an add-on injecting script into someone's browser produces violations
// we neither caused nor can fix — and mixing it in would bury the handful of reports
// that mean something.

// cspReportLimit bounds the table on screen. store.MaxDistinctCSPReports already
// caps the table itself, so this only matters as a guard against the page growing
// past what's readable.
const cspReportLimit = 200

func (s *Handlers) pageCSPReports(w http.ResponseWriter, r *http.Request) {
	// Default to page violations; "all" shows everything including extension noise.
	source := store.CSPReportSourcePage
	switch q := r.URL.Query().Get("source"); {
	case q == "all":
		source = ""
	case store.ValidCSPReportSource(q):
		source = q
	}

	rows, err := s.Store.ListCSPReports(r.Context(), source, cspReportLimit)
	if err != nil {
		s.ServerError(w, r, "could not load CSP reports", err)
		return
	}
	stats, err := s.Store.CSPReportStats(r.Context())
	if err != nil {
		s.ServerError(w, r, "could not load CSP reports", err)
		return
	}

	u, err := s.Store.GetUserByID(r.Context(), s.Auth.CurrentUserID(r.Context()))
	if err != nil {
		s.ServerError(w, r, "could not load account", err)
		return
	}

	s.Render(w, r, "admin_csp.html", map[string]any{
		"Reports":   rows,
		"Stats":     stats,
		"Source":    source,
		"ShowAll":   source == "",
		"Limit":     cspReportLimit,
		"Capacity":  store.MaxDistinctCSPReports,
		"Retention": web.CSPRetention,
		// Clearing is destructive, so the button only renders for the capability
		// that already governs granting capabilities (see the route table).
		"CanClear": u.CapManageUsers,
		"Flash":    r.URL.Query().Get("flash"),
	})
}

// handleClearCSPReports deletes stored violations — the triage action: after
// shipping a fix you clear the row and watch whether it comes back. It clears
// whatever the current filter shows, so the extension noise can be dropped without
// losing real reports (and vice versa).
func (s *Handlers) handleClearCSPReports(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	source := ""
	if q := r.FormValue("source"); store.ValidCSPReportSource(q) {
		source = q
	}
	n, err := s.Store.ClearCSPReports(r.Context(), source)
	if err != nil {
		s.ServerError(w, r, "could not clear CSP reports", err)
		return
	}
	// Worth an audit line: it's an admin deleting a security-relevant record.
	web.LogAudit(r, "csp reports cleared",
		"actor_user_id", s.Auth.CurrentUserID(r.Context()), "source", source, "removed", n)

	back := "/admin/csp"
	if source != "" {
		back += "?source=" + source
	}
	noun := "violations"
	if n == 1 {
		noun = "violation"
	}
	web.RedirectFlash(w, r, back, "flash", fmt.Sprintf("%d %s cleared.", n, noun))
}
