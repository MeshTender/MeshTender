package core

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/observe/observetest"
	"github.com/MeshTender/MeshTender/internal/store"
)

func netForm(name, weekday, start, duration, tz string) url.Values {
	return url.Values{"name": {name}, "weekday": {weekday}, "start": {start}, "duration": {duration},
		"timezone": {tz}, "active": {"1"}}
}

func TestOrgNetsAdmin(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, adminSess := appLogin(t, ts, st, ctx, h.app, "netsadmin")
	member, memberSess := appLogin(t, ts, st, ctx, h.app, "netsmember")
	_, outsiderSess := appLogin(t, ts, st, ctx, h.app, "netsoutsider")
	org, err := st.CreateOrg(ctx, "Nets Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	base := "/orgs/" + org.Slug + "/nets"

	if body := getBody(t, ts, h.app, base, memberSess); strings.Contains(body, `action="`+base+`"`) {
		t.Error("a member sees the create form")
	}
	if got := status(t, do(t, ts, h.app, base, outsiderSess)); got != http.StatusNotFound {
		t.Errorf("outsider GET = %d", got)
	}
	if got := status(t, post(t, ts, h.app, base, netForm("x", "0", "19:00", "60", "UTC"), memberSess)); got != http.StatusNotFound {
		t.Errorf("member create = %d", got)
	}

	for name, form := range map[string]url.Values{
		"no name":      netForm(" ", "0", "19:00", "60", "UTC"),
		"bad weekday":  netForm("x", "7", "19:00", "60", "UTC"),
		"bad time":     netForm("x", "0", "7pm", "60", "UTC"),
		"zero minutes": netForm("x", "0", "19:00", "0", "UTC"),
		"too long":     netForm("x", "0", "19:00", "1441", "UTC"),
		"no zone":      netForm("x", "0", "19:00", "60", ""),
		"bad zone":     netForm("x", "0", "19:00", "60", "Mars/Olympus"),
	} {
		if q := redirectTo(t, post(t, ts, h.app, base, form, adminSess), base); q.Get("error") == "" {
			t.Errorf("%s: accepted", name)
		}
	}

	resp := post(t, ts, h.app, base, netForm("Sunday Net", "0", "19:00", "60", "America/New_York"), adminSess)
	loc := resp.Header.Get("Location")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, base+"/") {
		t.Fatalf("create = %d → %q", resp.StatusCode, loc)
	}
	body := getBody(t, ts, h.app, loc, memberSess)
	if !strings.Contains(body, "Sundays, 19:00–20:00 (America/New_York)") || !strings.Contains(body, `data-testid="net-next"`) {
		t.Error("net page lacks its schedule or next session")
	}
	if !strings.Contains(getBody(t, ts, h.app, base, memberSess), "Sunday Net") {
		t.Error("list lacks the net")
	}

	// Edit, then delete.
	q := redirectTo(t, post(t, ts, h.app, loc, netForm("Monday Net", "1", "20:30", "30", "America/New_York"), adminSess), loc)
	if q.Get("ok") == "" {
		t.Error("no confirmation after saving")
	}
	if body := getBody(t, ts, h.app, loc, adminSess); !strings.Contains(body, "Mondays, 20:30–21:00") {
		t.Error("edit not applied")
	}
	if got := status(t, post(t, ts, h.app, loc+"/delete", nil, memberSess)); got != http.StatusNotFound {
		t.Errorf("member delete = %d", got)
	}
	redirectTo(t, post(t, ts, h.app, loc+"/delete", nil, adminSess), base)
	if got := status(t, do(t, ts, h.app, loc, adminSess)); got != http.StatusNotFound {
		t.Errorf("deleted net GET = %d", got)
	}
}

func TestNetSessionRosterPage(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "rosterpageadmin")
	org, err := st.CreateOrg(ctx, "Roster Page Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A net whose window is open right now, so a session is already running.
	now := time.Now().UTC()
	startMin := now.Hour()*60 + now.Minute() - 10
	if startMin < 0 {
		startMin = 0
	}
	in := store.NetInput{Name: "Now Net", Weekday: now.Weekday(), StartMinute: startMin, DurationMinutes: 120, Timezone: "UTC", Active: true}
	net, err := st.CreateNet(ctx, org.ID, in, admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := st.NextNetSession(ctx, net.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	// Registered and confirmed before the session started.
	heard, err := st.CreateCompanion(ctx, admin.ID, "Heard T1", observetest.NewIdentity(t).String())
	if err != nil {
		t.Fatal(err)
	}
	quiet, err := st.CreateCompanion(ctx, admin.ID, "Quiet T2", observetest.NewIdentity(t).String())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Second)
	for _, sg := range []store.AdvertSighting{
		{CompanionID: heard.ID, ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: at, AdvertTS: uint32(at.Unix()), RouteType: 1, PathHashSize: 1, Path: []byte{1, 2}}, //nolint:gosec // test clock
		{CompanionID: quiet.ID, ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: cur.StartsAt.Add(-time.Hour), AdvertTS: 1, RouteType: 1, PathHashSize: 1, Path: []byte{}},
	} {
		if _, err := st.RecordAdvertSighting(ctx, sg); err != nil {
			t.Fatal(err)
		}
	}
	// Quiet's only advert predates both its registration and the session, so
	// confirm it directly: it's a member companion that simply wasn't heard.
	if _, err := st.Pool().Exec(ctx, `UPDATE companions SET confirmed_at = now() WHERE id = $1`, quiet.ID); err != nil {
		t.Fatal(err)
	}

	path := "/orgs/" + org.Slug + "/nets/" + strconv.FormatInt(net.ID, 10) + "/sessions/" + strconv.FormatInt(cur.ID, 10)
	body := getBody(t, ts, h.app, path, sess)
	for _, want := range []string{"Heard T1", "Quiet T2", "Not heard", "In progress", "1 of 2 member companions checked in"} {
		if !strings.Contains(body, want) {
			t.Errorf("roster lacks %q", want)
		}
	}
	if !strings.Contains(getBody(t, ts, h.app, "/orgs/"+org.Slug+"/nets/"+strconv.FormatInt(net.ID, 10), sess), "Live roster") {
		t.Error("net page doesn't link the live roster")
	}

	// A session of another net, or one that hasn't started, is not found.
	other, err := st.CreateNet(ctx, org.ID, store.NetInput{Name: "Later", Weekday: (now.Weekday() + 3) % 7, StartMinute: 60,
		DurationMinutes: 60, Timezone: "UTC", Active: true}, admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	future, err := st.NextNetSession(ctx, other.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	otherBase := "/orgs/" + org.Slug + "/nets/" + strconv.FormatInt(other.ID, 10) + "/sessions/"
	for _, p := range []string{otherBase + strconv.FormatInt(future.ID, 10), otherBase + strconv.FormatInt(cur.ID, 10)} {
		if got := status(t, do(t, ts, h.app, p, sess)); got != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, got)
		}
	}
}
