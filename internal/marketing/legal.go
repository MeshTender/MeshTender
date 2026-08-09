package marketing

import "net/http"

// The privacy policy and terms. Both pages describe THIS deployment, so the
// operator details below are the one place to edit when they change — the
// templates never hardcode a name, address, or date.
//
// The privacy copy is deliberately specific (exact retention windows, exactly
// which third parties see what) because it can be: the implementation is
// first-party and narrow. Anything stated here is enforced by code elsewhere in
// the tree, and the retention figures are the same constants the janitor uses —
// if one changes, change it here too. TestPrivacyPageStatesRealRetention
// (internal/core/legal_pages_test.go) keeps the two honest about the windows it
// can check.

// operatorInfo is who runs this deployment, for the legal pages.
type operatorInfo struct {
	// Name is the person or entity responsible for the service.
	Name string
	// Contact is where privacy and account questions go.
	Contact string
	// Jurisdiction is the place whose law governs the terms ("" hides the clause
	// rather than claiming a jurisdiction that hasn't been chosen).
	Jurisdiction string
	// Effective is the date both documents took effect, as displayed.
	Effective string
}

// operator identifies who is responsible for this instance.
//
// A fork running its own deployment must change these: they name who is
// accountable for the service and where its users' privacy and account questions
// go, so shipping ours would point your users at us. Every field may be emptied
// instead — the affected sentences drop out rather than printing a placeholder at
// a reader — but a privacy policy with no contact route is not much of a promise.
//
// Jurisdiction names the law governing the terms, so it has to be a body of law
// that exists: a state or a country, not a city.
var operator = operatorInfo{
	Name:         "Jonathon Leight",
	Contact:      "legal@meshtender.com",
	Jurisdiction: "the State of New York, United States",
	Effective:    "31 July 2026",
}

// legalData is the shared render payload for both documents.
func (s *Handlers) legalData() map[string]any {
	return map[string]any{
		"Operator":     operator.Name,
		"Contact":      operator.Contact,
		"Jurisdiction": operator.Jurisdiction,
		"Effective":    operator.Effective,
	}
}

// pagePrivacy renders the privacy policy.
func (s *Handlers) pagePrivacy(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, "privacy.html", s.legalData())
}

// pageTerms renders the terms of use.
func (s *Handlers) pageTerms(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, "terms.html", s.legalData())
}
