//go:build browser

package e2e

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/config"
	"github.com/MeshTender/MeshTender/internal/observe/observetest"
	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2ECompanionRegistrationAndOrgPages registers a companion through the real
// form, simulates the advert the ingester would record, and walks the companion,
// activity and observers pages: the new forms, the timestamp localization and
// the data-confirm delete all have to work under the strict CSP.
func TestE2ECompanionRegistrationAndOrgPages(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2ecompanion")
	org, err := srv.store.CreateOrg(srv.ctx, "Checkin Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	rep := srv.newRepeater(t, user.ID, "Hilltop")
	key := observetest.NewIdentity(t).String()

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var pending bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the register form",
			chromedp.Navigate(srv.appURL+"/companions"),
			chromedp.Click(`[data-testid="companion-new"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`#companion-name`, chromedp.ByQuery)),
		step("register the companion",
			chromedp.SendKeys(`#companion-name`, "My T1", chromedp.ByQuery),
			chromedp.SendKeys(`#companion-key`, key, chromedp.ByQuery),
			chromedp.Submit(`#companion-name`, chromedp.ByQuery),
			waitForLocation(srv.appURL+"/companions/"),
			chromedp.WaitVisible(`[data-testid="companion-pending"]`, chromedp.ByQuery),
			chromedp.Evaluate(`!!document.querySelector('[data-testid="companion-pending"]')`, &pending)),
	); err != nil {
		t.Fatalf("register companion: %v", err)
	}
	if !pending {
		t.Fatal("no confirmation instructions after registering")
	}

	// What the ingester records when an observer hears the flood advert.
	list, err := srv.store.ListCompanionsForUser(srv.ctx, user.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("companions = %v, %v", list, err)
	}
	hash, _ := hex.DecodeString(rep.PublicKeyHex[:6])
	now := time.Now().Add(time.Second)
	if _, err := srv.store.RecordAdvertHeard(srv.ctx, store.AdvertHeard{
		CompanionID: list[0].ID, ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: now,
		AdvertTS: uint32(now.Unix()), Relays: [][]byte{hash}, //nolint:gosec // test clock
	}); err != nil {
		t.Fatal(err)
	}

	var companionText, activityText, observersText string
	if err := chromedp.Run(bctx,
		step("see the check-in on the companion page",
			chromedp.Reload(),
			chromedp.WaitVisible(`[data-testid="checkins"]`, chromedp.ByQuery),
			chromedp.Text(`#main-content`, &companionText, chromedp.ByQuery)),
		step("see it in the org's check-ins",
			chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug+"/activity"),
			chromedp.WaitVisible(`[data-testid="activity"]`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="activity"]`, &activityText, chromedp.ByQuery)),
		// The broker lives under Settings.
		step("open the org's Settings",
			chromedp.Click(`[data-testid="org-tab-settings"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="org-settings-nav"]`, chromedp.ByQuery)),
		step("open Settings › MQTT",
			chromedp.Click(`[data-testid="org-settings-mqtt"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="broker-card"]`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="broker-card"]`, &observersText, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("browse check-in pages: %v", err)
	}
	// Counts, never the repeaters that carried it.
	if strings.Contains(companionText, "Waiting for an advert") || !strings.Contains(companionText, "1 repeater") ||
		strings.Contains(companionText, "Hilltop") {
		t.Errorf("companion page after the advert: %q", companionText)
	}
	if !strings.Contains(activityText, "My T1") || strings.Contains(activityText, "Hilltop") {
		t.Errorf("activity: %q", activityText)
	}
	if !strings.Contains(observersText, "No broker configured") {
		t.Errorf("broker card: %q", observersText)
	}

	// Remove it through the data-confirm form.
	acceptDialogs(bctx)
	if err := chromedp.Run(bctx,
		step("open the companion",
			chromedp.Navigate(srv.appURL+"/companions/"+list[0].PublicID)),
		step("delete it",
			chromedp.Click(`form[action$="/delete"] button`, chromedp.ByQuery),
			// Back on the (now empty) list; the companion page's URL shares the
			// prefix, so wait for the list's empty state rather than the location.
			chromedp.WaitVisible(`.empty`, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("delete companion: %v", err)
	}
	if left, _ := srv.store.ListCompanionsForUser(srv.ctx, user.ID); len(left) != 0 {
		t.Error("companion not deleted")
	}
	watch.assertClean(t)
}

// TestE2ENetScheduleAndRoster creates a net through the real form (the
// time-zone picker runs in its required mode, so a zone must be preselected
// and there's no auto-detect option), then opens a live session's roster.
func TestE2ENetScheduleAndRoster(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2enets")
	org, err := srv.store.CreateOrg(srv.ctx, "Net Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var zone string
	var hasAuto bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the new-net form",
			chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug+"/settings/nets/new"),
			// Options in a closed select never count as visible; wait for the
			// picker to have rebuilt the list instead.
			chromedp.Poll(`document.querySelector("#net-timezone optgroup") !== null`, nil),
			chromedp.Evaluate(`document.getElementById("net-timezone").value`, &zone),
			chromedp.Evaluate(`!!document.querySelector('#net-timezone option[value=""]')`, &hasAuto)),
		step("create the net",
			chromedp.SendKeys(`#net-name`, "Sunday Net", chromedp.ByQuery),
			chromedp.Submit(`#net-name`, chromedp.ByQuery),
			// A new net lands on its settings page.
			chromedp.WaitVisible(`[data-testid="net-schedule-form"]`, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("create net: %v", err)
	}
	if zone == "" || hasAuto {
		t.Errorf("time-zone picker: value %q, auto-detect option present %v", zone, hasAuto)
	}

	// A net whose window is open now, with one companion heard in it.
	now := time.Now().UTC()
	in := store.NetInput{Name: "Live Net", Weekday: now.Weekday(), StartMinute: max(0, now.Hour()*60+now.Minute()-10),
		DurationMinutes: 120, Timezone: "UTC", Active: true}
	net, err := srv.store.CreateNet(srv.ctx, org.ID, in, user.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := srv.store.NextNetSession(srv.ctx, net.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	c, err := srv.store.CreateCompanion(srv.ctx, user.ID, "Live T1", observetest.NewIdentity(t).String())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Second)
	if _, err := srv.store.RecordAdvertHeard(srv.ctx, store.AdvertHeard{CompanionID: c.ID,
		ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: at, AdvertTS: uint32(at.Unix()), //nolint:gosec // test clock
		ZeroHop: true}); err != nil {
		t.Fatal(err)
	}

	var roster string
	if err := chromedp.Run(bctx,
		step("open the live session's roster",
			chromedp.Navigate(fmt.Sprintf("%s/orgs/%s/nets/%d/sessions/%d", srv.appURL, org.Slug, net.ID, sess.ID)),
			chromedp.WaitVisible(`[data-testid="roster"]`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="roster"]`, &roster, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("roster: %v", err)
	}
	if !strings.Contains(roster, "Live T1") || !strings.Contains(roster, "Zero-hop") {
		t.Errorf("roster: %q", roster)
	}
	watch.assertClean(t)
}

// TestE2ENetControlAcknowledgesAndRosterRefreshes: on a manual-acknowledgement
// net, net control clicks Acknowledge, and the in-progress roster picks up a
// check-in that arrives while the page is open (htmx polling, under the CSP).
func TestE2ENetControlAcknowledgesAndRosterRefreshes(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2enetcontrol")
	org, err := srv.store.CreateOrg(srv.ctx, "Ack Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	now := time.Now().UTC()
	net, err := srv.store.CreateNet(srv.ctx, org.ID, store.NetInput{Name: "Ack Net", Weekday: now.Weekday(),
		StartMinute: max(0, now.Hour()*60+now.Minute()-10), DurationMinutes: 120, Timezone: "UTC", Active: true, ManualAck: true}, user.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := srv.store.NextNetSession(srv.ctx, net.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	checkIn := func(owner int64, name string) {
		c, err := srv.store.CreateCompanion(srv.ctx, owner, name, observetest.NewIdentity(t).String())
		if err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(time.Second)
		if _, err := srv.store.RecordAdvertHeard(srv.ctx, store.AdvertHeard{CompanionID: c.ID,
			ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: at, AdvertTS: uint32(at.Unix())}); err != nil { //nolint:gosec // test clock
			t.Fatal(err)
		}
	}
	checkIn(user.ID, "NC T1")

	bctx, cancel, watch := startBrowser(t)
	defer cancel()
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the session",
			chromedp.Navigate(fmt.Sprintf("%s/orgs/%s/nets/%d/sessions/%d", srv.appURL, org.Slug, net.ID, sess.ID)),
			chromedp.WaitVisible(`[data-testid="acknowledge"]`, chromedp.ByQuery)),
		step("acknowledge the check-in",
			chromedp.Click(`[data-testid="acknowledge"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="acked"]`, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}

	// Another member checks in while the page sits open.
	late, err := srv.store.CreateUser(srv.ctx, "e2elatecomer", "Late Comer")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.AddOrgMember(srv.ctx, org.ID, late.ID, "member"); err != nil {
		t.Fatal(err)
	}
	checkIn(late.ID, "Late T1")
	if err := chromedp.Run(bctx,
		// Slow on purpose: an in-progress roster refreshes every 10s (htmx
		// polling), so this can take up to two of those.
		stepWithin("see the roster pick up the late check-in", 25*time.Second,
			chromedp.Poll(`document.querySelector('[data-testid="roster"]').innerText.includes("Late Comer")`, nil)),
	); err != nil {
		t.Fatalf("roster didn't refresh with the new check-in: %v", err)
	}
	watch.assertClean(t)
}

// TestE2ENetControlAddsCheckIn: net control finds a member who wasn't heard
// with the "Add a check-in" search (htmx, under the CSP), sees their status, and
// adds them; the session log shows the hand-added check-in.
func TestE2ENetControlAddsCheckIn(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2enetadd")
	org, err := srv.store.CreateOrg(srv.ctx, "Add Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	radioless, err := srv.store.CreateUser(srv.ctx, "e2eradioless", "Radioless Rita")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.AddOrgMember(srv.ctx, org.ID, radioless.ID, "member"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	net, err := srv.store.CreateNet(srv.ctx, org.ID, store.NetInput{Name: "Add Net", Weekday: now.Weekday(),
		StartMinute: max(0, now.Hour()*60+now.Minute()-10), DurationMinutes: 120, Timezone: "UTC", Active: true}, user.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := srv.store.NextNetSession(srv.ctx, net.ID, now)
	if err != nil {
		t.Fatal(err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()
	var status, added string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the session",
			chromedp.Navigate(fmt.Sprintf("%s/orgs/%s/nets/%d/sessions/%d", srv.appURL, org.Slug, net.ID, sess.ID)),
			chromedp.WaitVisible(`[data-testid="add-search"]`, chromedp.ByQuery)),
		step("search for the member",
			chromedp.SendKeys(`[data-testid="add-search"]`, "rita", chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="add-member"]`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="add-status"]`, &status, chromedp.ByQuery)),
		step("add their check-in by hand",
			chromedp.SetValue(`#add-note`, "on the phone", chromedp.ByQuery),
			chromedp.Click(`[data-testid="add-member"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="added-by-hand"]`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="roster"]`, &added, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("add a check-in: %v", err)
	}
	if !strings.Contains(status, "no companion") {
		t.Errorf("search status = %q", status)
	}
	if !strings.Contains(added, "Radioless Rita") || !strings.Contains(added, "on the phone") {
		t.Errorf("session log = %q", added)
	}
	watch.assertClean(t)
}

// TestE2ENetAirSettings: an admin saves a net's on-air settings through the
// real form in Settings › Nets — choosing Hashtag reveals only the hashtag
// field — and the net's page summarizes them.
func TestE2ENetAirSettings(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2enetair")
	org, err := srv.store.CreateOrg(srv.ctx, "Air Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	net, err := srv.store.CreateNet(srv.ctx, org.ID, store.NetInput{Name: "Air Net", Weekday: time.Sunday, StartMinute: 19 * 60,
		DurationMinutes: 60, Timezone: "UTC", Active: true}, user.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.CreateRegion(srv.ctx, org.ID, store.RegionInput{Token: "us-ny", DisplayName: "New York", AllowFlood: true}); err != nil {
		t.Fatal(err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()
	const hidden = `document.querySelector('[data-air-field="%s"]').classList.contains("d-none")`
	var hashtagHiddenBefore, hashtagHiddenAfter, privateHiddenAfter bool
	var summary, page string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the net's settings",
			chromedp.Navigate(fmt.Sprintf("%s/orgs/%s/settings/nets/%d", srv.appURL, org.Slug, net.ID)),
			chromedp.WaitVisible(`[data-testid="net-air-save"]`, chromedp.ByQuery),
			chromedp.Evaluate(fmt.Sprintf(hidden, "hashtag"), &hashtagHiddenBefore)),
		step("choose a hashtag channel",
			chromedp.Click(`[data-testid="air-kind-hashtag"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`#air-hashtag`, chromedp.ByQuery),
			chromedp.Evaluate(fmt.Sprintf(hidden, "hashtag"), &hashtagHiddenAfter),
			chromedp.Evaluate(fmt.Sprintf(hidden, "private"), &privateHiddenAfter)),
		step("save the on-air settings",
			chromedp.SetValue(`#air-hashtag`, "wnynet", chromedp.ByQuery),
			chromedp.Evaluate(`(() => { const s = document.getElementById("air-scope"); s.value = s.options[1].value; })()`, nil),
			chromedp.SetValue(`#air-sender`, "Net Control", chromedp.ByQuery),
			chromedp.Click(`[data-testid="net-air-save"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="flash-ok"]`, chromedp.ByQuery)),
		step("see them summarized on the net's page",
			chromedp.Navigate(fmt.Sprintf("%s/orgs/%s/nets/%d", srv.appURL, org.Slug, net.ID)),
			chromedp.WaitVisible(`[data-testid="net-air-summary"]`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="net-air-summary"]`, &summary, chromedp.ByQuery),
			chromedp.Text(`.page-body`, &page, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("save on-air settings: %v", err)
	}
	if !hashtagHiddenBefore || hashtagHiddenAfter || !privateHiddenAfter {
		t.Errorf("channel fields: hashtag hidden %v → %v, private hidden after %v", hashtagHiddenBefore, hashtagHiddenAfter, privateHiddenAfter)
	}
	for _, want := range []string{"#wnynet", "us-ny"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q: %q", want, summary)
		}
	}
	if !strings.Contains(page, "news:") {
		t.Error("the net page doesn't say how to add to the session log")
	}
	if a, err := srv.store.GetNetAir(srv.ctx, org.ID, net.ID); err != nil || a.OnAirName != "Net Control" {
		t.Errorf("on-air settings = %+v, %v", a, err)
	}
	watch.assertClean(t)
}

// TestE2ENetModemPanel: on a live session net control gets the modem panel
// (its scripts load under the CSP; the serial connect itself needs hardware),
// and the roster shows who was acknowledged on the air and the channel log.
func TestE2ENetModemPanel(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2enetmodem")
	org, err := srv.store.CreateOrg(srv.ctx, "Modem Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := srv.store.SetOrgRadio(srv.ctx, org.ID, &config.RadioDefaults{FreqHz: 910_525_000, BwHz: 62_500, SF: 7, CR: 5}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	net, err := srv.store.CreateNet(srv.ctx, org.ID, store.NetInput{Name: "Modem Net", Weekday: now.Weekday(),
		StartMinute: max(0, now.Hour()*60+now.Minute()-10), DurationMinutes: 120, Timezone: "UTC", Active: true}, user.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := srv.store.NextNetSession(srv.ctx, net.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	c, err := srv.store.CreateCompanion(srv.ctx, user.ID, "Air T1", observetest.NewIdentity(t).String())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Second)
	if _, err := srv.store.RecordAdvertHeard(srv.ctx, store.AdvertHeard{CompanionID: c.ID,
		ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: at, AdvertTS: uint32(at.Unix())}); err != nil { //nolint:gosec // test clock
		t.Fatal(err)
	}
	if err := srv.store.MarkAired(srv.ctx, sess.ID, []int64{user.ID}, at); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.AddNetSessionMessage(srv.ctx, sess.ID, []byte("p"), "Bob", "road closed", at); err != nil {
		t.Fatal(err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()
	var connectShown bool
	var roster, channelLog string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the live session",
			chromedp.Navigate(fmt.Sprintf("%s/orgs/%s/nets/%d/sessions/%d", srv.appURL, org.Slug, net.ID, sess.ID)),
			chromedp.WaitVisible(`[data-testid="net-modem"]`, chromedp.ByQuery)),
		step("read the modem panel, roster and channel log",
			chromedp.Evaluate(`!!document.querySelector('[data-testid="nc-connect"]') && typeof window.MeshBridge === "object"`, &connectShown),
			chromedp.Text(`[data-testid="roster"]`, &roster, chromedp.ByQuery),
			chromedp.Text(`[data-testid="channel-log"]`, &channelLog, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("modem panel: %v", err)
	}
	if !connectShown {
		t.Error("connect button or MeshBridge missing")
	}
	if !strings.Contains(roster, "copied on air") {
		t.Errorf("roster: %q", roster)
	}
	if !strings.Contains(channelLog, "Bob") || !strings.Contains(channelLog, "road closed") || strings.Contains(channelLog, "unverified") {
		t.Errorf("channel log: %q", channelLog)
	}
	watch.assertClean(t)
}
