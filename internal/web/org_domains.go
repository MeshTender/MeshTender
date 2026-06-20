package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jleight/meshtender/internal/store"
)

// txtRecordName is the DNS label an org adds under its domain to prove ownership.
const txtRecordPrefix = "_meshtender."

// normalizeHostname lowercases and strips any scheme, path, port, or trailing
// dot from user-entered domain input, returning "" if it isn't a plausible host.
func normalizeHostname(raw string) string {
	h := strings.TrimSpace(strings.ToLower(raw))
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	if i := strings.IndexAny(h, "/:"); i >= 0 {
		h = h[:i]
	}
	h = strings.TrimSuffix(h, ".")
	// Require at least one dot and only host-legal characters.
	if !strings.Contains(h, ".") || strings.ContainsAny(h, " \t") {
		return ""
	}
	for _, c := range h {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return ""
		}
	}
	return h
}

// handleAddOrgDomain registers a new custom domain for an org (admin only).
func (s *Server) handleAddOrgDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	host := normalizeHostname(r.FormValue("hostname"))
	if host == "" {
		orgErr(w, r, "Enter a valid domain (e.g. mesh.example.org).")
		return
	}
	if _, err := s.store.CreateOrgDomain(r.Context(), id, host); errors.Is(err, store.ErrDuplicate) {
		orgErr(w, r, "That domain is already claimed.")
		return
	} else if err != nil {
		orgErr(w, r, "Could not add domain.")
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r), http.StatusSeeOther)
}

// handleVerifyOrgDomain checks the org's DNS TXT record carries the domain's
// verification token, then marks it verified (admin only).
func (s *Server) handleVerifyOrgDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	domainID, err := strconv.ParseInt(r.FormValue("domain_id"), 10, 64)
	if err != nil {
		orgErr(w, r, "Invalid domain.")
		return
	}
	d, err := s.store.GetOrgDomain(r.Context(), id, domainID)
	if err != nil {
		orgErr(w, r, "Unknown domain.")
		return
	}
	records, err := s.lookupTXT(txtRecordPrefix + d.Hostname)
	if err != nil {
		orgErr(w, r, "Could not read DNS TXT records yet — they may still be propagating.")
		return
	}
	if !txtRecordsHaveToken(records, d.VerificationToken) {
		orgErr(w, r, "TXT record not found yet. Double-check the value, then retry.")
		return
	}
	if err := s.store.MarkOrgDomainVerified(r.Context(), id, domainID); err != nil {
		orgErr(w, r, "Could not save verification.")
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r), http.StatusSeeOther)
}

// txtRecordsHaveToken reports whether any TXT record exactly matches the token
// (after trimming surrounding whitespace).
func txtRecordsHaveToken(records []string, token string) bool {
	for _, rec := range records {
		if strings.TrimSpace(rec) == token {
			return true
		}
	}
	return false
}

// handleDeleteOrgDomain removes a custom domain (admin only).
func (s *Server) handleDeleteOrgDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	domainID, err := strconv.ParseInt(r.FormValue("domain_id"), 10, 64)
	if err != nil {
		orgErr(w, r, "Invalid domain.")
		return
	}
	if err := s.store.DeleteOrgDomain(r.Context(), id, domainID); err != nil {
		orgErr(w, r, "Could not remove domain.")
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r), http.StatusSeeOther)
}
