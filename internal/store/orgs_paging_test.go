package store

import (
	"fmt"
	"testing"
	"time"
)

// TestListPublicOrgsPageByName walks the name-sorted directory and checks every
// org appears exactly once, in (name, id) order, across pages.
func TestListPublicOrgsPageByName(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	owner, err := st.CreateUser(ctx, "pager", "")
	if err != nil {
		t.Fatal(err)
	}
	// Create more orgs than a single page so we exercise the seek across pages.
	total := OrgsPageSize*2 + 3
	for i := 0; i < total; i++ {
		// Zero-padded names give a deterministic lexical order to assert against.
		if _, err := st.CreateOrg(ctx, fmt.Sprintf("org-%04d", i), owner.ID); err != nil {
			t.Fatalf("create org %d: %v", i, err)
		}
	}

	var (
		seen     = map[int64]bool{}
		count    int
		p        = OrgListParams{Sort: OrgSortName}
		prevName string
		pages    int
	)
	for {
		page, hasMore, err := st.ListPublicOrgsPage(ctx, p)
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		pages++
		if len(page) > OrgsPageSize {
			t.Fatalf("page returned %d rows, exceeds OrgsPageSize %d", len(page), OrgsPageSize)
		}
		for _, o := range page {
			if seen[o.ID] {
				t.Fatalf("org %d (%q) returned twice", o.ID, o.Name)
			}
			seen[o.ID] = true
			if prevName != "" && o.Name < prevName {
				t.Fatalf("out of order: %q after %q", o.Name, prevName)
			}
			prevName = o.Name
			count++
		}
		if !hasMore {
			break
		}
		last := page[len(page)-1]
		p.HasCursor, p.AfterName, p.AfterID = true, last.Name, last.ID
	}

	if count != total {
		t.Errorf("walked %d orgs across %d pages, want %d", count, pages, total)
	}
	if pages < 3 {
		t.Errorf("expected at least 3 pages for %d orgs, got %d", total, pages)
	}
}

// TestListPublicOrgsPageByMembers checks the default ordering puts orgs with the
// most members first, and that the count-keyset seek pages without dupes.
func TestListPublicOrgsPageByMembers(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	// Give each org a distinct member count: org i ends up with i+1 members
	// (the creator, plus i extra members).
	const n = 6
	for i := 0; i < n; i++ {
		owner, err := st.CreateUser(ctx, fmt.Sprintf("owner-%d", i), "")
		if err != nil {
			t.Fatal(err)
		}
		org, err := st.CreateOrg(ctx, fmt.Sprintf("org-%d", i), owner.ID)
		if err != nil {
			t.Fatalf("create org %d: %v", i, err)
		}
		for j := 0; j < i; j++ {
			u, err := st.CreateUser(ctx, fmt.Sprintf("m-%d-%d", i, j), "")
			if err != nil {
				t.Fatal(err)
			}
			if err := st.AddOrgMember(ctx, org.ID, u.ID, "member"); err != nil {
				t.Fatalf("add member: %v", err)
			}
		}
	}

	page, _, err := st.ListPublicOrgsPage(ctx, OrgListParams{Sort: OrgSortMembers})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page) != n {
		t.Fatalf("got %d orgs, want %d", len(page), n)
	}
	for i := 1; i < len(page); i++ {
		if page[i-1].MemberCount < page[i].MemberCount {
			t.Errorf("not member-descending: %d before %d", page[i-1].MemberCount, page[i].MemberCount)
		}
	}
	if page[0].MemberCount != n {
		t.Errorf("top org has %d members, want %d", page[0].MemberCount, n)
	}
}

// TestListPublicOrgsPageSearch checks the search term matches name, description,
// and region case-insensitively, and that wildcards are treated literally.
func TestListPublicOrgsPageSearch(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	owner, err := st.CreateUser(ctx, "searcher", "")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name, desc, region string) {
		o, err := st.CreateOrg(ctx, name, owner.ID)
		if err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
		if err := st.UpdateOrg(ctx, o.ID, o.Slug, name, desc, region); err != nil {
			t.Fatalf("update %q: %v", name, err)
		}
	}
	mk("Cascade Mesh", "Pacific Northwest repeaters", "Oregon")
	mk("Desert Net", "100% coverage", "Arizona")
	mk("Random Club", "no match here", "Texas")

	find := func(q string) []string {
		page, _, err := st.ListPublicOrgsPage(ctx, OrgListParams{Sort: OrgSortName, Query: q})
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		var names []string
		for _, o := range page {
			names = append(names, o.Name)
		}
		return names
	}

	if got := find("cascade"); len(got) != 1 || got[0] != "Cascade Mesh" {
		t.Errorf(`search "cascade" = %v, want [Cascade Mesh]`, got)
	}
	if got := find("ARIZONA"); len(got) != 1 || got[0] != "Desert Net" {
		t.Errorf(`search "ARIZONA" (region, case-insensitive) = %v, want [Desert Net]`, got)
	}
	if got := find("northwest"); len(got) != 1 || got[0] != "Cascade Mesh" {
		t.Errorf(`search "northwest" (description) = %v, want [Cascade Mesh]`, got)
	}
	// "%" is a literal, not a wildcard: it matches only Desert Net's "100%".
	if got := find("%"); len(got) != 1 || got[0] != "Desert Net" {
		t.Errorf(`search "%%" (literal) = %v, want [Desert Net]`, got)
	}
}

// TestListPublicOrgsPageNextNet: each directory row carries how many active nets
// the org runs and the one starting soonest — not an inactive net, even one
// that would be sooner, and nothing for an org without nets.
func TestListPublicOrgsPageNextNet(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner := mustUser(t, st, ctx, "dirnets")
	withNets := mustOrg(t, st, ctx, "Has Nets", owner)
	without := mustOrg(t, st, ctx, "No Nets", owner)

	now := time.Now()
	in := func(name string, startsIn time.Duration, active bool) NetInput {
		start := now.Add(startsIn).UTC()
		return NetInput{Name: name, Weekday: start.Weekday(), StartMinute: start.Hour()*60 + start.Minute(),
			DurationMinutes: 30, Timezone: "UTC", Active: active}
	}
	for _, n := range []NetInput{in("Later", 5*time.Hour, true), in("Sooner", 2*time.Hour, true), in("Paused", time.Hour, false)} {
		if _, err := st.CreateNet(ctx, withNets, n, owner, now); err != nil {
			t.Fatal(err)
		}
	}

	page, _, err := st.ListPublicOrgsPage(ctx, OrgListParams{Sort: OrgSortName, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]OrgSummary{}
	for _, o := range page {
		byID[o.ID] = o
	}
	got := byID[withNets]
	if got.NextNet == nil || got.NextNet.Name != "Sooner" || got.NetCount != 2 || got.OtherNets() != 1 {
		t.Errorf("org with nets: NextNet %+v, NetCount %d, want Sooner of 2", got.NextNet, got.NetCount)
	}
	if got.NextNet != nil && got.NextNet.Schedule() == "" {
		t.Error("next net has no schedule")
	}
	if o := byID[without]; o.NextNet != nil || o.NetCount != 0 || o.OtherNets() != 0 {
		t.Errorf("org without nets: NextNet %+v, NetCount %d, want none", o.NextNet, o.NetCount)
	}
}
