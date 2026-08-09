package marketing

import (
	"strings"
	"testing"
)

// TestOperatorIsIdentified: the legal pages drop any sentence whose operator field
// is empty, which is right for a fork that hasn't filled them in but wrong for a
// live deployment — a privacy policy that names nobody and offers no contact route
// promises nothing, and it fails silently, rendering a clean page either way.
//
// So this asserts the deployment declares a responsible party. A fork that wants
// the fields blank is changing this test on purpose, which is the point: it makes
// "we publish a policy with no accountable party" a deliberate act rather than an
// oversight.
func TestOperatorIsIdentified(t *testing.T) {
	t.Parallel()

	if operator.Name == "" {
		t.Error("operator.Name is empty, so the privacy policy and terms name nobody as responsible")
	}
	if operator.Contact == "" {
		t.Error("operator.Contact is empty, so a user with a privacy or account question has nowhere to send it")
	} else if !strings.Contains(operator.Contact, "@") || strings.ContainsAny(operator.Contact, " <>") {
		// The terms render it as a mailto: link, so a non-address produces a dead one.
		t.Errorf("operator.Contact = %q, which is rendered into mailto: and must be a bare email address", operator.Contact)
	}
	if operator.Jurisdiction == "" {
		t.Error("operator.Jurisdiction is empty, so the terms state no governing law")
	}
	if operator.Effective == "" {
		t.Error("operator.Effective is empty; both documents display the date they took effect")
	}
}
