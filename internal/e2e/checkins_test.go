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
		chromedp.Navigate(srv.appURL+"/companions"),
		chromedp.SendKeys(`#companion-name`, "My T1", chromedp.ByQuery),
		chromedp.SendKeys(`#companion-key`, key, chromedp.ByQuery),
		chromedp.Submit(`#companion-name`, chromedp.ByQuery),
		waitForLocation(srv.appURL+"/companions/"),
		chromedp.WaitVisible(`[data-testid="companion-pending"]`, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('[data-testid="companion-pending"]')`, &pending),
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
	if _, err := srv.store.RecordAdvertSighting(srv.ctx, store.AdvertSighting{
		CompanionID: list[0].ID, ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: now,
		AdvertTS: uint32(now.Unix()), RouteType: 1, PathHashSize: 3, Path: hash, //nolint:gosec // test clock
	}); err != nil {
		t.Fatal(err)
	}

	var companionText, activityText, observersText string
	if err := chromedp.Run(bctx,
		chromedp.Reload(),
		chromedp.WaitVisible(`[data-testid="sightings"]`, chromedp.ByQuery),
		chromedp.Text(`#main-content`, &companionText, chromedp.ByQuery),
		chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug+"/activity"),
		chromedp.WaitVisible(`[data-testid="activity"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="activity"]`, &activityText, chromedp.ByQuery),
		chromedp.Click(`[data-testid="org-tab-observers"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="broker-card"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="broker-card"]`, &observersText, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browse check-in pages: %v", err)
	}
	if strings.Contains(companionText, "Waiting for an advert") || !strings.Contains(companionText, "Hilltop") {
		t.Errorf("companion page after the advert: %q", companionText)
	}
	if !strings.Contains(activityText, "My T1") || !strings.Contains(activityText, "Hilltop") {
		t.Errorf("activity: %q", activityText)
	}
	if !strings.Contains(observersText, "No broker configured") {
		t.Errorf("broker card: %q", observersText)
	}

	// Remove it through the data-confirm form.
	acceptDialogs(bctx)
	if err := chromedp.Run(bctx,
		chromedp.Navigate(srv.appURL+"/companions/"+list[0].PublicID),
		chromedp.Click(`form[action$="/delete"] button`, chromedp.ByQuery),
		// Back on the (now empty) list; the companion page's URL shares the
		// prefix, so wait for the list's empty state rather than the location.
		chromedp.WaitVisible(`.empty`, chromedp.ByQuery),
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
		chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug+"/nets"),
		// Options in a closed select never count as visible; wait for the
		// picker to have rebuilt the list instead.
		chromedp.Poll(`document.querySelector("#net-timezone optgroup") !== null`, nil, chromedp.WithPollingTimeout(10*time.Second)),
		chromedp.Evaluate(`document.getElementById("net-timezone").value`, &zone),
		chromedp.Evaluate(`!!document.querySelector('#net-timezone option[value=""]')`, &hasAuto),
		chromedp.SendKeys(`#net-name`, "Sunday Net", chromedp.ByQuery),
		chromedp.Submit(`#net-name`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="net-next"]`, chromedp.ByQuery),
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
	if _, err := srv.store.RecordAdvertSighting(srv.ctx, store.AdvertSighting{CompanionID: c.ID,
		ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: at, AdvertTS: uint32(at.Unix()), //nolint:gosec // test clock
		RouteType: 2, PathHashSize: 1, Path: []byte{}}); err != nil {
		t.Fatal(err)
	}

	var roster string
	if err := chromedp.Run(bctx,
		chromedp.Navigate(fmt.Sprintf("%s/orgs/%s/nets/%d/sessions/%d", srv.appURL, org.Slug, net.ID, sess.ID)),
		chromedp.WaitVisible(`[data-testid="roster"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="roster"]`, &roster, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("roster: %v", err)
	}
	if !strings.Contains(roster, "Live T1") || !strings.Contains(roster, "Zero-hop") {
		t.Errorf("roster: %q", roster)
	}
	watch.assertClean(t)
}
