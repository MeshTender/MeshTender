package store

import (
	"strings"
	"testing"
	"time"
)

// TestListOrgMembersLastAdvert: each member carries when an advert from any of
// their companions was last heard — the newest of them — and nil for a member
// none of whose companions has been heard.
func TestListOrgMembersLastAdvert(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	heard := mustUser(t, st, ctx, "advert-heard")
	quiet := mustUser(t, st, ctx, "advert-quiet")
	org := mustOrg(t, st, ctx, "AdvertOrg", heard)
	if err := st.AddOrgMember(ctx, org, quiet, "member"); err != nil {
		t.Fatal(err)
	}
	older := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	newer := older.Add(48 * time.Hour)
	for i, at := range []time.Time{older, newer} {
		c, err := st.CreateCompanion(ctx, heard, "Radio", strings.Repeat(string(rune('a'+i)), 64))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.pool.Exec(ctx, `UPDATE companions SET last_heard_at = $2 WHERE id = $1`, c.ID, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CreateCompanion(ctx, quiet, "Silent", strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}

	members, err := st.ListOrgMembers(ctx, org)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]*time.Time{}
	for _, m := range members {
		got[m.UserID] = m.LastAdvertAt
	}
	if at := got[heard]; at == nil || !at.Equal(newer) {
		t.Errorf("heard member's last advert = %v, want %v (the newest of their companions)", at, newer)
	}
	if at := got[quiet]; at != nil {
		t.Errorf("quiet member's last advert = %v, want nil", at)
	}
}
