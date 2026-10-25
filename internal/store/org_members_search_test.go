package store

import (
	"slices"
	"testing"
)

// TestSearchOrgMembers: members match on username or display name, case-
// insensitively and literally (a "%" is no wildcard); other orgs' people don't.
func TestSearchOrgMembers(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "searchadmin")
	alice := mustUser(t, st, ctx, "alice_a")
	outsider := mustUser(t, st, ctx, "alice_outside")
	org := mustOrg(t, st, ctx, "SearchOrg", admin)
	mustOrg(t, st, ctx, "SearchOther", outsider)
	if err := st.AddOrgMember(ctx, org, alice, "member"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(ctx, `UPDATE users SET display_name = 'Alice Ames' WHERE id = $1`, alice); err != nil {
		t.Fatal(err)
	}
	names := func(q string, limit int) []string {
		t.Helper()
		got, err := st.SearchOrgMembers(ctx, org, q, limit)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range got {
			out = append(out, m.Username)
		}
		return out
	}
	if got := names("AMES", 10); !slices.Equal(got, []string{"alice_a"}) {
		t.Errorf("by display name = %v", got)
	}
	if got := names("alice", 10); !slices.Equal(got, []string{"alice_a"}) {
		t.Errorf("by username = %v (no outsiders)", got)
	}
	if got := names("%", 10); len(got) != 0 {
		t.Errorf(`"%%" matched %v`, got)
	}
	if got := names("", 1); len(got) != 1 {
		t.Errorf("limit 1 = %v", got)
	}
}
