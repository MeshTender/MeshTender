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
		if q := redirectTo(t, post(t, ts, h.app, base, form, adminSess), "/orgs/"+org.Slug+"/settings/nets/new"); q.Get("error") == "" {
			t.Errorf("%s: accepted", name)
		}
	}

	// Creating a net lands on its settings, where the on-air settings are.
	created := post(t, ts, h.app, base, netForm("Sunday Net", "0", "19:00", "60", "America/New_York"), adminSess)
	settingsPrefix := "/orgs/" + org.Slug + "/settings/nets/"
	created.Body.Close()
	createdAt, _ := url.Parse(created.Header.Get("Location"))
	if created.StatusCode != http.StatusSeeOther || createdAt == nil || !strings.HasPrefix(createdAt.Path, settingsPrefix) {
		t.Fatalf("create = %d → %q", created.StatusCode, created.Header.Get("Location"))
	}
	settings := createdAt.Path
	loc := base + "/" + strings.TrimPrefix(settings, settingsPrefix)
	if body := getBody(t, ts, h.app, settings, adminSess); !strings.Contains(body, `data-testid="net-schedule-form"`) {
		t.Error("the net's settings page lacks its schedule form")
	}
	if got := status(t, do(t, ts, h.app, settings, memberSess)); got != http.StatusNotFound {
		t.Errorf("member GET of the net's settings = %d", got)
	}
	body := getBody(t, ts, h.app, loc, memberSess)
	if !strings.Contains(body, "Sundays, 19:00–20:00 (America/New_York)") || !strings.Contains(body, `data-testid="net-next"`) {
		t.Error("net page lacks its schedule or next session")
	}
	if !strings.Contains(getBody(t, ts, h.app, base, memberSess), "Sunday Net") {
		t.Error("list lacks the net")
	}
	// Creating nets is Settings' job: Check-ins › Nets doesn't link there, even
	// for an admin, once there are nets to list.
	if body := getBody(t, ts, h.app, base, adminSess); strings.Contains(body, `href="/orgs/`+org.Slug+`/settings/nets`) {
		t.Error("Check-ins › Nets links to Settings › Nets")
	}

	// Edit, then delete. A rejected schedule is re-shown with what was entered.
	if body := readBody(t, post(t, ts, h.app, loc, netForm("Typo Net", "1", "20:30", "0", "America/New_York"), adminSess)); !strings.Contains(body, `data-testid="settings-form-error"`) ||
		!strings.Contains(body, `value="Typo Net"`) {
		t.Error("a rejected schedule isn't re-shown with its error")
	}
	q := redirectTo(t, post(t, ts, h.app, loc, netForm("Monday Net", "1", "20:30", "30", "America/New_York"), adminSess), settings)
	if q.Get("ok") == "" {
		t.Error("no confirmation after saving")
	}
	if body := getBody(t, ts, h.app, loc, adminSess); !strings.Contains(body, "Mondays, 20:30–21:00") {
		t.Error("edit not applied")
	}
	// The acknowledgement mode is its own section, and a schedule save leaves it.
	if got := status(t, post(t, ts, h.app, loc+"/ack", url.Values{"ack": {"manual"}}, memberSess)); got != http.StatusNotFound {
		t.Errorf("member ack-mode save = %d", got)
	}
	redirectTo(t, post(t, ts, h.app, loc+"/ack", url.Values{"ack": {"manual"}}, adminSess), settings)
	redirectTo(t, post(t, ts, h.app, loc, netForm("Monday Net", "1", "20:30", "30", "America/New_York"), adminSess), settings)
	nid, _ := strconv.ParseInt(strings.TrimPrefix(settings, settingsPrefix), 10, 64)
	if n, err := st.GetNet(ctx, org.ID, nid); err != nil || !n.ManualAck {
		t.Errorf("ack mode after a schedule save = %+v, %v; want manual", n, err)
	}
	if got := status(t, post(t, ts, h.app, loc+"/delete", nil, memberSess)); got != http.StatusNotFound {
		t.Errorf("member delete = %d", got)
	}
	redirectTo(t, post(t, ts, h.app, loc+"/delete", nil, adminSess), "/orgs/"+org.Slug+"/settings/nets")
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
	// The admin's second device: one person, one roster row.
	if _, err := st.CreateCompanion(ctx, admin.ID, "Spare T3", observetest.NewIdentity(t).String()); err != nil {
		t.Fatal(err)
	}
	quietUser, err := st.CreateUser(ctx, "rosterquiet", "Quiet Member")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, quietUser.ID, "member"); err != nil {
		t.Fatal(err)
	}
	quiet, err := st.CreateCompanion(ctx, quietUser.ID, "Quiet T2", observetest.NewIdentity(t).String())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Second)
	for _, h := range []store.AdvertHeard{
		{CompanionID: heard.ID, ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: at, AdvertTS: uint32(at.Unix()), Relays: [][]byte{{1}, {2}}}, //nolint:gosec // test clock
		{CompanionID: quiet.ID, ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: cur.StartsAt.Add(-time.Hour), AdvertTS: 1},
	} {
		if _, err := st.RecordAdvertHeard(ctx, h); err != nil {
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
	for _, want := range []string{"rosterpageadmin", "on <a href=\"/companions/" + heard.PublicID + "\">Heard T1</a>",
		"Quiet Member", "Not heard", "In progress", "1 of 2 members checked in", ">Spread<", "2 repeaters"} {
		if !strings.Contains(body, want) {
			t.Errorf("roster lacks %q", want)
		}
	}
	// By member, not device: the quiet member's companion and the admin's
	// spare aren't rows of their own.
	for _, absent := range []string{"Quiet T2", "Spare T3"} {
		if strings.Contains(body, absent) {
			t.Errorf("roster lists the device %q", absent)
		}
	}
	if !strings.Contains(getBody(t, ts, h.app, "/orgs/"+org.Slug+"/nets/"+strconv.FormatInt(net.ID, 10), sess), `data-testid="net-live-roster"`) {
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

// TestNetControlAcknowledgements walks a manual-acknowledgement net from the
// web: only admins designate net control; only net control (or an admin)
// acknowledges or logs check-ins by hand; the roster shows each state; and the
// in-progress roster's htmx poll gets just the roster card.
func TestNetControlAcknowledgements(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, adminSess := appLogin(t, ts, st, ctx, h.app, "ackadmin")
	control, controlSess := appLogin(t, ts, st, ctx, h.app, "ackcontrol")
	heardUser, err := st.CreateUser(ctx, "ackheard", "Heard Member")
	if err != nil {
		t.Fatal(err)
	}
	radioless, err := st.CreateUser(ctx, "ackradioless", "Radioless Member")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "Ack Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []int64{control.ID, heardUser.ID, radioless.ID} {
		if err := st.AddOrgMember(ctx, org.ID, u, "member"); err != nil {
			t.Fatal(err)
		}
	}
	base := "/orgs/" + org.Slug + "/nets"

	// A manual-acknowledgement net created through the form, open right now.
	now := time.Now().UTC()
	form := netForm("Ack Net", strconv.Itoa(int(now.Weekday())),
		time.Date(2000, 1, 1, 0, max(0, now.Hour()*60+now.Minute()-10), 0, 0, time.UTC).Format("15:04"), "120", "UTC")
	form.Set("ack", "manual")
	resp := post(t, ts, h.app, base, form, adminSess)
	resp.Body.Close()
	created, _ := url.Parse(resp.Header.Get("Location"))
	nid, _ := strconv.ParseInt(created.Path[strings.LastIndex(created.Path, "/")+1:], 10, 64)
	netPath := base + "/" + strconv.FormatInt(nid, 10)
	settingsPath := netSettingsURL(org.Slug, nid)
	net, err := st.GetNet(ctx, org.ID, nid)
	if err != nil || !net.ManualAck {
		t.Fatalf("net = %+v, %v; want manual acknowledgement", net, err)
	}
	cur, err := st.NextNetSession(ctx, nid, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sessPath := netPath + "/sessions/" + strconv.FormatInt(cur.ID, 10)

	// The heard member's advert during the session.
	comp, err := st.CreateCompanion(ctx, heardUser.ID, "Heard T1", observetest.NewIdentity(t).String())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Second)
	if _, err := st.RecordAdvertHeard(ctx, store.AdvertHeard{CompanionID: comp.ID, AdvertTS: uint32(at.Unix()), //nolint:gosec // test clock
		ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: at}); err != nil {
		t.Fatal(err)
	}

	// Before designation the member is just a member: no controls, no posting.
	if body := getBody(t, ts, h.app, sessPath, controlSess); strings.Contains(body, `data-testid="acknowledge"`) || strings.Contains(body, `data-testid="add-checkin"`) {
		t.Error("a plain member sees net control's controls")
	}
	if got := status(t, post(t, ts, h.app, sessPath+"/acks", url.Values{"user_id": {strconv.FormatInt(heardUser.ID, 10)}}, controlSess)); got != http.StatusNotFound {
		t.Errorf("plain member acknowledging = %d, want 404", got)
	}
	if got := status(t, post(t, ts, h.app, netPath+"/controls", url.Values{"user_id": {strconv.FormatInt(control.ID, 10)}}, controlSess)); got != http.StatusNotFound {
		t.Errorf("member designating net control = %d, want 404", got)
	}
	redirectTo(t, post(t, ts, h.app, netPath+"/controls", url.Values{"user_id": {strconv.FormatInt(control.ID, 10)}}, adminSess), settingsPath)
	if body := getBody(t, ts, h.app, settingsPath, adminSess); !strings.Contains(body, "@ackcontrol") {
		t.Error("the net's settings don't list the designated net control")
	}
	if body := getBody(t, ts, h.app, netPath, controlSess); !strings.Contains(body, "plus ackcontrol") {
		t.Error("the net page doesn't name the designated net control")
	}

	// Net control sees the heard advert awaiting acknowledgement, then
	// acknowledges it and logs the radioless member by hand.
	body := getBody(t, ts, h.app, sessPath, controlSess)
	if !strings.Contains(body, `data-testid="awaiting"`) || !strings.Contains(body, `data-testid="acknowledge"`) {
		t.Fatal("heard check-in isn't awaiting acknowledgement for net control")
	}
	redirectTo(t, post(t, ts, h.app, sessPath+"/acks", url.Values{"user_id": {strconv.FormatInt(heardUser.ID, 10)}}, controlSess), sessPath)
	redirectTo(t, post(t, ts, h.app, sessPath+"/acks", url.Values{"user_id": {strconv.FormatInt(radioless.ID, 10)}, "note": {"with traffic"}}, controlSess), sessPath)

	body = getBody(t, ts, h.app, sessPath, adminSess)
	for _, want := range []string{"acknowledged by @ackcontrol", "Radioless Member", "by @ackcontrol", "with traffic", "2 of 2 members checked in"} {
		if !strings.Contains(body, want) {
			t.Errorf("roster lacks %q", want)
		}
	}
	if strings.Contains(body, `data-testid="awaiting"`) {
		t.Error("still awaiting after acknowledgement")
	}

	// The htmx poll gets just the roster card, no page chrome.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+sessPath, nil)
	req.Host = h.app
	req.Header.Set("HX-Request", "true")
	req.AddCookie(adminSess)
	frag, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	fb := readBody(t, frag)
	if !strings.Contains(fb, `id="roster-card"`) || strings.Contains(fb, "<html") || strings.Contains(fb, `data-testid="add-checkin"`) {
		t.Errorf("htmx roster response isn't just the card: %.200s", fb)
	}
	if !strings.Contains(body, `hx-trigger="every 10s"`) {
		t.Error("in-progress roster doesn't poll")
	}

	// Undo the hand-added check-in.
	redirectTo(t, post(t, ts, h.app, sessPath+"/acks/"+strconv.FormatInt(radioless.ID, 10)+"/delete", nil, controlSess), sessPath)
	// (Their name is back in the add-a-check-in picker, so look at the roster cell.)
	if body := getBody(t, ts, h.app, sessPath, adminSess); strings.Contains(body, "<td>Radioless Member</td>") {
		t.Error("undone hand-added check-in still on the roster")
	}

	// Net control deletes their account: their acknowledgements stay, credited
	// to "@<net-control>" rather than "by @" with nothing after it.
	redirectTo(t, post(t, ts, h.app, sessPath+"/acks", url.Values{"user_id": {strconv.FormatInt(radioless.ID, 10)}}, controlSess), sessPath)
	if err := st.DeleteUser(ctx, control.ID); err != nil {
		t.Fatal(err)
	}
	body = getBody(t, ts, h.app, sessPath, adminSess)
	if !strings.Contains(body, `data-testid="acked"`) || !strings.Contains(body, `data-testid="added-by-hand"`) {
		t.Fatal("acknowledgements vanished with net control's account")
	}
	if strings.Count(body, "by @&lt;net-control&gt;") != 2 {
		t.Error("roster doesn't credit a deleted net control as @<net-control>")
	}
}

// TestAutomaticAcknowledgementShowsNoAwaiting: on a default net an observed
// advert needs nothing more from net control.
func TestAutomaticAcknowledgementShowsNoAwaiting(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "autoadmin")
	org, err := st.CreateOrg(ctx, "Auto Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	net, err := st.CreateNet(ctx, org.ID, store.NetInput{Name: "Auto", Weekday: now.Weekday(), StartMinute: max(0, now.Hour()*60+now.Minute()-10),
		DurationMinutes: 120, Timezone: "UTC", Active: true}, admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := st.NextNetSession(ctx, net.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	comp, err := st.CreateCompanion(ctx, admin.ID, "Auto T1", observetest.NewIdentity(t).String())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Second)
	if _, err := st.RecordAdvertHeard(ctx, store.AdvertHeard{CompanionID: comp.ID, AdvertTS: uint32(at.Unix()), //nolint:gosec // test clock
		ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: at}); err != nil {
		t.Fatal(err)
	}
	body := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/nets/"+strconv.FormatInt(net.ID, 10)+"/sessions/"+strconv.FormatInt(cur.ID, 10), sess)
	if !strings.Contains(body, "on <a") || strings.Contains(body, `data-testid="awaiting"`) || strings.Contains(body, `data-testid="acknowledge"`) {
		t.Error("automatic net shows a manual-acknowledgement state")
	}
}
