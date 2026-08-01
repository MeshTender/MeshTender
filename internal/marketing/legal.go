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
// if one changes, change it here too. TestLegalPagesStateRealRetention keeps the
// two honest about the windows it can check.

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
// TODO(operator): fill these in before the public launch. The pages render
// without them — the affected sentences simply drop out rather than printing a
// placeholder at a reader — but a privacy policy with no contact route is not
// much of a promise.
var operator = operatorInfo{
	Name:         "",
	Contact:      "",
	Jurisdiction: "",
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
