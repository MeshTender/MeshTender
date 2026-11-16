package core

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/store"
)

// netAround is a weekly UTC net whose window starts at start, an hour long.
func netAround(name string, start time.Time) store.NetInput {
	start = start.UTC()
	return store.NetInput{Name: name, Weekday: start.Weekday(), StartMinute: start.Hour()*60 + start.Minute(),
		DurationMinutes: 60, Timezone: "UTC", Active: true}
}

// TestOrgHomeEmpty: a new org's Home has no live banner, says no nets are coming
// up (pointing an admin at Settings), has an empty Recent activity, and nudges a
// member with no companion toward the Guide. It carries none of what moved off
// Home: About, counts, the configuration card, the map.
func TestOrgHomeEmpty(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, adminSess := appLogin(t, ts, st, ctx, h.app, "homeadmin")
	member, memberSess := appLogin(t, ts, st, ctx, h.app, "homemember")
	org, err := st.CreateOrg(ctx, "Home Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	home := "/orgs/" + org.Slug

	body := getBody(t, ts, h.app, home, memberSess)
	for _, absent := range []string{`data-testid="home-live-net"`, `data-testid="home-upcoming-net"`,
		`data-testid="home-about"`, `data-testid="home-counts"`, `data-testid="home-observers"`,
		`data-testid="org-config-card"`, `id="map"`, "Schedule one under Settings"} {
		if strings.Contains(body, absent) {
			t.Errorf("member's empty Home has %s", absent)
		}
	}
	for _, want := range []string{"No nets coming up", "Nothing yet.",
		`data-testid="home-companion-nudge"`, `href="/orgs/` + org.Slug + `/guide" data-testid="home-companion-nudge-link"`} {
		if !strings.Contains(body, want) {
			t.Errorf("member's empty Home lacks %q", want)
		}
	}

	if body := getBody(t, ts, h.app, home, adminSess); !strings.Contains(body, "Schedule one under Settings") {
		t.Error("admin's Home doesn't point at Settings to schedule a net")
	}
}

// TestOrgHomeNets: a net in session gets a banner linking to its roster and a
// "started" activity entry; the nets still to come are listed soonest first,
// each linking to its page, without repeating the one in session; and a member
// with a registered companion isn't nudged.
func TestOrgHomeNets(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, adminSess := appLogin(t, ts, st, ctx, h.app, "homenets")
	org, err := st.CreateOrg(ctx, "Home Nets Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	live, err := st.CreateNet(ctx, org.ID, netAround("Live Net", now.Add(-10*time.Minute)), admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	later, err := st.CreateNet(ctx, org.ID, netAround("Later Net", now.Add(5*time.Hour)), admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateNet(ctx, org.ID, netAround("Sooner Net", now.Add(2*time.Hour)), admin.ID, now); err != nil {
		t.Fatal(err)
	}
	session, err := st.NextNetSession(ctx, live.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCompanion(ctx, admin.ID, "Home T1", strings.Repeat("cd", 32)); err != nil {
		t.Fatal(err)
	}

	body := getBody(t, ts, h.app, "/orgs/"+org.Slug, adminSess)
	roster := `/orgs/` + org.Slug + `/nets/` + strconv.FormatInt(live.ID, 10) + `/sessions/` + strconv.FormatInt(session.ID, 10)
	_, banner, found := strings.Cut(body, `data-testid="home-live-net"`)
	if !found || !strings.Contains(banner, "Live Net") || !strings.Contains(banner, `href="`+roster+`"`) {
		t.Error("no live banner linking to the roster")
	}
	if !strings.Contains(body, `data-testid="home-activity-net_started"`) {
		t.Error("the session in progress isn't in Recent activity")
	}
	_, upcoming, _ := strings.Cut(body, `data-testid="home-upcoming"`)
	upcoming, _, _ = strings.Cut(upcoming, `data-testid="home-activity"`)
	assertInOrder(t, "Upcoming nets", upcoming, "Sooner Net", "Later Net")
	if strings.Contains(upcoming, "Live Net") {
		t.Error("Upcoming nets repeats the net already in session")
	}
	if !strings.Contains(upcoming, `href="/orgs/`+org.Slug+`/nets/`+strconv.FormatInt(later.ID, 10)+`"`) {
		t.Error("an upcoming net doesn't link to its page")
	}
	if strings.Contains(body, `data-testid="home-companion-nudge"`) {
		t.Error("a member with a companion is still nudged to register one")
	}
}
