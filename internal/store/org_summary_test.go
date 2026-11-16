package store

import (
	"strings"
	"testing"
)

// TestOrgSummary: an org's one-line summary is saved with its profile, read back
// wherever the org is (GetOrg, the directory, a member's list), matched by the
// directory search, and capped by the column itself at MaxOrgSummary characters
// — counted in characters, not bytes.
func TestOrgSummary(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner := mustUser(t, st, ctx, "summaryowner")
	id := mustOrg(t, st, ctx, "Summary Org", owner)
	org, err := st.GetOrg(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	p := OrgProfile{Slug: org.Slug, Name: org.Name, Summary: "Weekly check-in net along the lakeshore.",
		Description: "A **long** about.", Region: "Lakeshore"}
	if err := st.UpdateOrg(ctx, id, p); err != nil {
		t.Fatal(err)
	}
	if got, err := st.GetOrg(ctx, id); err != nil || got.Summary != p.Summary || got.Description != p.Description {
		t.Fatalf("GetOrg = %+v, %v; want summary %q", got, err, p.Summary)
	}
	page, _, err := st.ListPublicOrgsPage(ctx, OrgListParams{Sort: OrgSortName, Query: "lakeshore"})
	if err != nil || len(page) != 1 || page[0].Summary != p.Summary {
		t.Fatalf("directory search on the summary = %+v, %v", page, err)
	}
	mine, err := st.ListOrgsForUser(ctx, owner)
	if err != nil || len(mine) != 1 || mine[0].Org.Summary != p.Summary || mine[0].Org.Region != "Lakeshore" {
		t.Fatalf("ListOrgsForUser = %+v, %v; want the summary and region", mine, err)
	}

	p.Summary = strings.Repeat("é", MaxOrgSummary) // two bytes each, but at the limit in characters
	if err := st.UpdateOrg(ctx, id, p); err != nil {
		t.Fatalf("summary of exactly %d characters: %v", MaxOrgSummary, err)
	}
	p.Summary += "é"
	if err := st.UpdateOrg(ctx, id, p); err == nil {
		t.Fatalf("summary of %d characters was saved", MaxOrgSummary+1)
	}
}
