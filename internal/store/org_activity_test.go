package store

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// TestListOrgActivity: an org's recent activity is its participating repeaters
// (by when they joined it) and its nets' started sessions — in progress ones as
// "started", finished ones as "ended" with their check-in count — newest first
// and capped; other orgs, opted-out repeaters, and future sessions stay out.
func TestListOrgActivity(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "actadmin")
	outsider := mustUser(t, st, ctx, "actoutsider")
	org := mustOrg(t, st, ctx, "ActOrg", admin)
	other := mustOrg(t, st, ctx, "ActOther", outsider)

	if got, err := st.ListOrgActivity(ctx, org, time.Now(), 8); err != nil || len(got) != 0 {
		t.Fatalf("empty org activity = %+v, %v", got, err)
	}

	repeater := func(owner int64, name string, key byte) int64 {
		t.Helper()
		r, err := st.CreateRepeater(ctx, &Repeater{OwnerID: owner, Name: name, PublicKeyHex: strings.Repeat(string(key), 64),
			RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5})
		if err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	repeater(admin, "Hilltop", 'a')
	optedOut := repeater(admin, "Basement", 'b')
	if err := st.SetRepeaterOrgExcluded(ctx, org, optedOut, true); err != nil {
		t.Fatal(err)
	}
	repeater(outsider, "Elsewhere", 'c')

	// Sessions inserted at fixed times, after the repeaters' creation (stamped by
	// the database's clock) so the order doesn't depend on clock skew.
	at := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	in := testNetInput()
	in.Active = false // no materialized sessions; the test inserts its own
	net, err := st.CreateNet(ctx, org, in, admin, at)
	if err != nil {
		t.Fatal(err)
	}
	otherNet, err := st.CreateNet(ctx, other, in, outsider, at)
	if err != nil {
		t.Fatal(err)
	}
	session := func(netID int64, start, end time.Time) int64 {
		t.Helper()
		var id int64
		if err := st.pool.QueryRow(ctx, `INSERT INTO net_sessions (net_id, starts_at, ends_at) VALUES ($1, $2, $3) RETURNING id`,
			netID, start, end).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	ended := session(net.ID, at.Add(-3*time.Hour), at.Add(-2*time.Hour))
	live := session(net.ID, at.Add(-30*time.Minute), at.Add(30*time.Minute))
	session(net.ID, at.Add(24*time.Hour), at.Add(25*time.Hour)) // future
	otherEnded := session(otherNet.ID, at.Add(-time.Hour), at.Add(-time.Minute))
	if err := st.AcknowledgeMember(ctx, org, ended, admin, admin, "by hand", at.Add(-150*time.Minute)); err != nil {
		t.Fatal(err)
	}

	got, err := st.ListOrgActivity(ctx, org, at, 8)
	if err != nil {
		t.Fatal(err)
	}
	want := []OrgActivity{
		{Kind: ActivityNetStarted, At: at.Add(-30 * time.Minute), Name: "Weekly Net", NetID: net.ID, SessionID: live},
		{Kind: ActivityNetEnded, At: at.Add(-2 * time.Hour), Name: "Weekly Net", NetID: net.ID, SessionID: ended, CheckedIn: 1},
		{Kind: ActivityRepeaterAdded, Name: "Hilltop"},
	}
	if len(got) != len(want) {
		t.Fatalf("activity = %+v, want %d entries", got, len(want))
	}
	for i := range want {
		g := got[i]
		if want[i].Kind == ActivityRepeaterAdded {
			g.At = time.Time{} // stamped by the database
		}
		if !g.At.Equal(want[i].At) {
			t.Errorf("entry %d at %v, want %v", i, g.At, want[i].At)
		}
		g.At, want[i].At = time.Time{}, time.Time{}
		if g != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, g, want[i])
		}
	}

	if got, _ := st.ListOrgActivity(ctx, org, at, 2); len(got) != 2 || got[1].Kind != ActivityNetEnded {
		t.Fatalf("capped at 2 = %+v", got)
	}

	counts, err := st.NetSessionsCheckedIn(ctx, org, []int64{ended, live, otherEnded})
	if err != nil {
		t.Fatal(err)
	}
	if counts[ended] != 1 || counts[live] != 0 {
		t.Fatalf("counts = %v", counts)
	}
	if _, ok := counts[otherEnded]; ok {
		t.Fatalf("another org's session counted: %v", counts)
	}
}

func TestGetOrgConfigSummary(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "sumadmin")
	org := mustOrg(t, st, ctx, "SumOrg", admin)

	if sum, err := st.GetOrgConfigSummary(ctx, org); err != nil || len(sum.Profiles) != 0 || sum.HasRegions {
		t.Fatalf("empty summary = %+v, %v", sum, err)
	}
	for _, name := range []string{"RAK 4631", "Heltec V3"} {
		if _, err := st.CreateProfile(ctx, org, name, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CreateRegion(ctx, org, RegionInput{Token: "wny", DisplayName: "WNY", Layer: 1}); err != nil {
		t.Fatal(err)
	}
	sum, err := st.GetOrgConfigSummary(ctx, org)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sum.Profiles, []string{"RAK 4631", "Heltec V3"}) || !sum.HasRegions {
		t.Fatalf("summary = %+v", sum)
	}
}

// TestListUserNetSummaries: a user's nets span their orgs (active ones only, not
// other orgs'), soonest session first, each tagged with its org.
func TestListUserNetSummaries(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	alice := mustUser(t, st, ctx, "usernetsalice")
	bob := mustUser(t, st, ctx, "usernetsbob")
	a := mustOrg(t, st, ctx, "UserNets A", alice)
	b := mustOrg(t, st, ctx, "UserNets B", alice)
	other := mustOrg(t, st, ctx, "UserNets Other", bob)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, ny) // Friday
	mk := func(org int64, name string, day time.Weekday, active bool) {
		t.Helper()
		in := testNetInput()
		in.Name, in.Weekday, in.Active = name, day, active
		if _, err := st.CreateNet(ctx, org, in, alice, now); err != nil {
			t.Fatal(err)
		}
	}
	mk(a, "Sunday A", time.Sunday, true)
	mk(b, "Saturday B", time.Saturday, true)
	mk(b, "Paused B", time.Monday, false)
	mk(other, "Not Mine", time.Saturday, true)

	got, err := st.ListUserNetSummaries(ctx, alice, now)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, n := range got {
		names = append(names, n.Name+"@"+n.OrgName)
	}
	if strings.Join(names, ",") != "Saturday B@UserNets B,Sunday A@UserNets A" {
		t.Fatalf("nets = %v", names)
	}
	if got[0].Next == nil || got[0].OrgSlug == "" {
		t.Fatalf("first = %+v", got[0])
	}
}
