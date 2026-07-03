package core

import (
	"strings"
	"testing"
)

// TestOrgDescriptionRendersMarkdown verifies the public org page renders the org
// description through the markdown pipeline (sanitized HTML), not as raw source.
func TestOrgDescriptionRendersMarkdown(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, err := st.CreateUser(ctx, "mdowner", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "Markdown Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrg(ctx, org.ID, org.Slug, org.Name, "We run **many** repeaters.\n\n- one\n- two", "NA"); err != nil {
		t.Fatal(err)
	}

	body := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug))
	if !strings.Contains(body, "<strong>many</strong>") {
		t.Fatal("org description did not render markdown emphasis (<strong>many</strong> missing)")
	}
	if !strings.Contains(body, "<li>one</li>") {
		t.Fatal("org description did not render a markdown list")
	}
	if strings.Contains(body, "**many**") {
		t.Fatal("raw markdown source leaked into the rendered page")
	}
}
