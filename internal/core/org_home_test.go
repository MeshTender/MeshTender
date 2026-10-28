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

// TestOrgHomeEmpty: a new org's Home has no live banner and no next nets, an
// empty Recent activity, and the observer count only for admins.
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
	for _, absent := range []string{`data-testid="home-live-net"`, `data-testid="home-upcoming"`, `data-testid="home-observers"`,
		`data-testid="org-config-card"`, `href="/orgs/` + org.Slug + `/settings"`} {
		if strings.Contains(body, absent) {
			t.Errorf("member's empty Home has %s", absent)
		}
	}
	if !strings.Contains(body, "Nothing yet.") {
		t.Error("empty Recent activity has no placeholder")
	}

	body = getBody(t, ts, h.app, home, adminSess)
	if !strings.Contains(body, `data-testid="home-observers"`) || !strings.Contains(body, "None registered") {
		t.Error("admin's Home lacks the observer count")
	}
	if !strings.Contains(body, `Add one in Settings`) {
		t.Error("admin's Home doesn't point at Settings for a missing description")
	}
}

// TestOrgHomeNetsAndSetup: a net in session gets a banner linking to its
// roster and a "started" activity entry; the next nets are listed; and the
// Recommended configuration card shows the org radio and profile names.
func TestOrgHomeNetsAndSetup(t *testing.T) {
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
	if _, err := st.CreateNet(ctx, org.ID, netAround("Later Net", now.Add(3*time.Hour)), admin.ID, now); err != nil {
		t.Fatal(err)
	}
	session, err := st.NextNetSession(ctx, live.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	radio := defaultRadio()
	if err := st.SetOrgRadio(ctx, org.ID, &radio); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProfile(ctx, org.ID, "Solar node", nil); err != nil {
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
	if _, upcoming, _ := strings.Cut(body, `data-testid="home-upcoming"`); !strings.Contains(upcoming, "Later Net") {
		t.Error("Next nets lacks the upcoming net")
	}
	if !strings.Contains(body, `data-testid="home-radio">`+radio.MHz()+` MHz`) || !strings.Contains(body, presetName(radio)) {
		t.Error("the Recommended configuration card lacks the org radio")
	}
	if !strings.Contains(body, `data-testid="home-profiles">Solar node<`) {
		t.Error("the Recommended configuration card lacks the profile names")
	}
}
