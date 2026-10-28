package core

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/observe"
	"github.com/MeshTender/MeshTender/internal/observe/observetest"
	"github.com/MeshTender/MeshTender/internal/secretbox"
	"github.com/MeshTender/MeshTender/internal/store"
)

// redirectTo asserts a 303 to path and returns the Location's query.
func redirectTo(t *testing.T, resp *http.Response, path string) url.Values {
	t.Helper()
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusSeeOther || loc == nil || loc.Path != path {
		t.Fatalf("got %d → %q, want 303 → %s", resp.StatusCode, resp.Header.Get("Location"), path)
	}
	return loc.Query()
}

func status(t *testing.T, resp *http.Response) int {
	t.Helper()
	resp.Body.Close()
	return resp.StatusCode
}

func TestCompanionRegistration(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	alice, aliceSess := appLogin(t, ts, st, ctx, h.app, "compalice")
	_, bobSess := appLogin(t, ts, st, ctx, h.app, "compbob")
	key := observetest.NewIdentity(t).String()

	// Pasted in uppercase with whitespace: stored in the canonical lowercase
	// form, or the ingester (which looks up lowercase hex) would never match it.
	resp := post(t, ts, h.app, "/companions", url.Values{"name": {"Alice T1"}, "public_key": {"  " + strings.ToUpper(key) + "\n"}}, aliceSess)
	loc := resp.Header.Get("Location")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/companions/") {
		t.Fatalf("add = %d → %q", resp.StatusCode, loc)
	}
	list, err := st.ListCompanionsForUser(ctx, alice.ID)
	if err != nil || len(list) != 1 || list[0].PublicKeyHex != key {
		t.Fatalf("stored = %+v, %v", list, err)
	}

	page := getBody(t, ts, h.app, loc, aliceSess)
	if !strings.Contains(page, `data-testid="companion-pending"`) || !strings.Contains(page, "flood") {
		t.Error("owner's unconfirmed companion page lacks the flood-advert instructions")
	}
	if !strings.Contains(getBody(t, ts, h.app, "/companions", aliceSess), "Alice T1") {
		t.Error("list lacks the companion")
	}

	for name, form := range map[string]url.Values{
		"bad key":   {"name": {"x"}, "public_key": {"abcd"}},
		"no name":   {"name": {"  "}, "public_key": {key}},
		"duplicate": {"name": {"Mine now"}, "public_key": {key}},
	} {
		q := redirectTo(t, post(t, ts, h.app, "/companions", form, bobSess), "/companions/new")
		if q.Get("error") == "" {
			t.Errorf("%s: no error message", name)
		}
	}
}

func TestCompanionVisibilityAndDelete(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	alice, aliceSess := appLogin(t, ts, st, ctx, h.app, "visalice")
	bob, bobSess := appLogin(t, ts, st, ctx, h.app, "visbob")
	_, eveSess := appLogin(t, ts, st, ctx, h.app, "viseve")
	org, err := st.CreateOrg(ctx, "Vis Org", alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, bob.ID, "member"); err != nil {
		t.Fatal(err)
	}
	c, err := st.CreateCompanion(ctx, alice.ID, "Alice T1", observetest.NewIdentity(t).String())
	if err != nil {
		t.Fatal(err)
	}
	path := "/companions/" + c.PublicID

	if body := getBody(t, ts, h.app, path, bobSess); strings.Contains(body, `data-testid="companion-pending"`) || strings.Contains(body, "/delete") {
		t.Error("an org-mate sees owner-only controls")
	}
	if got := status(t, do(t, ts, h.app, path, eveSess)); got != http.StatusNotFound {
		t.Errorf("outsider GET = %d, want 404", got)
	}
	for _, sess := range []*http.Cookie{bobSess, eveSess} {
		if got := status(t, post(t, ts, h.app, path+"/delete", nil, sess)); got != http.StatusNotFound {
			t.Errorf("non-owner delete = %d, want 404", got)
		}
	}
	redirectTo(t, post(t, ts, h.app, path+"/delete", nil, aliceSess), "/companions")
	if list, _ := st.ListCompanionsForUser(ctx, alice.ID); len(list) != 0 {
		t.Fatal("companion not deleted")
	}
}

// TestCompanionPageShowsCountsNotPaths: a companion's check-ins show when it
// was heard, by how many observers and how far the advert spread, and nothing
// that says which repeaters carried it, for the owner and org-mates alike.
func TestCompanionPageShowsCountsNotPaths(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	alice, sess := appLogin(t, ts, st, ctx, h.app, "routealice")
	bob, bobSess := appLogin(t, ts, st, ctx, h.app, "routebob")
	org, err := st.CreateOrg(ctx, "Route Org", alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, bob.ID, "member"); err != nil {
		t.Fatal(err)
	}
	rep := observetest.NewIdentity(t).String()
	if _, err := st.CreateRepeater(ctx, &store.Repeater{OwnerID: alice.ID, Name: "Hilltop", PublicKeyHex: rep}); err != nil {
		t.Fatal(err)
	}
	c, err := st.CreateCompanion(ctx, alice.ID, "Alice T1", observetest.NewIdentity(t).String())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	repHash, _ := hex.DecodeString(rep[:6])
	for _, h := range []store.AdvertHeard{
		{CompanionID: c.ID, AdvertTS: uint32(now.Unix()), ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: now, //nolint:gosec // test clock
			Relays: [][]byte{repHash, {0x99, 0x99, 0x99}}},
		{CompanionID: c.ID, AdvertTS: uint32(now.Unix()) + 60, ObserverPublicKeyHex: strings.Repeat("cd", 32), //nolint:gosec // test clock
			ReceivedAt: now.Add(time.Minute), ZeroHop: true},
	} {
		if _, err := st.RecordAdvertHeard(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	for who, cookie := range map[string]*http.Cookie{"owner": sess, "org-mate": bobSess} {
		body := getBody(t, ts, h.app, "/companions/"+c.PublicID, cookie)
		for _, want := range []string{`data-testid="checkins"`, "2 repeaters", "Zero-hop"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: companion page lacks %q", who, want)
			}
		}
		for _, leak := range []string{"Hilltop", "999999", rep[:6]} {
			if strings.Contains(body, leak) {
				t.Errorf("%s: companion page reveals %q, a repeater on the advert's path", who, leak)
			}
		}
	}
}

func TestOrgObserversAndBrokerAdmin(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, adminSess := appLogin(t, ts, st, ctx, h.app, "obsadmin")
	member, memberSess := appLogin(t, ts, st, ctx, h.app, "obsmember")
	_, outsiderSess := appLogin(t, ts, st, ctx, h.app, "obsoutsider")
	org, err := st.CreateOrg(ctx, "Obs Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	base := "/orgs/" + org.Slug
	obsKey := observetest.NewIdentity(t).String()

	// Settings › MQTT is for admins: members and outsiders get 404.
	for _, sess := range []*http.Cookie{memberSess, outsiderSess} {
		for _, p := range []string{"/settings/mqtt", "/settings/mqtt/observers/new"} {
			if got := status(t, do(t, ts, h.app, base+p, sess)); got != http.StatusNotFound {
				t.Errorf("non-admin GET %s = %d, want 404", p, got)
			}
		}
	}
	for _, p := range []string{"/observers", "/broker", "/broker/test", "/broker/delete"} {
		form := url.Values{"name": {"x"}, "public_key": {obsKey}, "url": {"tcp://203.0.113.10"}}
		if got := status(t, post(t, ts, h.app, base+p, form, memberSess)); got != http.StatusNotFound {
			t.Errorf("member POST %s = %d, want 404", p, got)
		}
	}

	redirectTo(t, post(t, ts, h.app, base+"/observers", url.Values{"name": {"Roof"}, "public_key": {strings.ToUpper(obsKey)}}, adminSess), base+"/settings/mqtt")
	if ok, _ := st.IsOrgObserver(ctx, org.ID, obsKey); !ok {
		t.Fatal("observer not registered under its lowercase key")
	}
	if q := redirectTo(t, post(t, ts, h.app, base+"/observers", url.Values{"name": {"Roof"}, "public_key": {obsKey}}, adminSess), base+"/settings/mqtt/observers/new"); q.Get("error") == "" {
		t.Error("duplicate observer accepted")
	}

	// The broker form refuses addresses the ingester would refuse to dial.
	for name, form := range map[string]url.Values{
		"loopback":     {"url": {"tcp://127.0.0.1:1883"}},
		"cluster IP":   {"url": {"tcp://10.93.111.151:1883"}},
		"metadata":     {"url": {"tcp://169.254.169.254"}},
		"bad scheme":   {"url": {"http://203.0.113.10"}},
		"credentials":  {"url": {"tcp://u:p@203.0.113.10"}},
		"wildcard pfx": {"url": {"tcp://203.0.113.10"}, "topic_prefix": {"meshcore/#"}},
	} {
		q := redirectTo(t, post(t, ts, h.app, base+"/broker", form, adminSess), base+"/settings/mqtt")
		if q.Get("error") == "" {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := st.GetOrgBroker(ctx, org.ID); err == nil {
		t.Fatal("a refused broker was saved")
	}

	form := url.Values{"url": {"tls://203.0.113.10:8883"}, "username": {"meshtender"}, "password": {"hunter2"}, "enabled": {"1"}}
	redirectTo(t, post(t, ts, h.app, base+"/broker", form, adminSess), base+"/settings/mqtt")
	b, err := st.GetOrgBroker(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := secretbox.Open(testMasterKey, b.PasswordSealed); err != nil || string(pt) != "hunter2" {
		t.Fatalf("password not sealed under the master key: %q, %v", pt, err)
	}
	if strings.Contains(string(b.PasswordSealed), "hunter2") || b.TopicPrefix != "meshcore" || !b.Enabled {
		t.Fatalf("broker = %+v", b)
	}
	// The page never echoes the password back.
	if body := getBody(t, ts, h.app, base+"/settings/mqtt", adminSess); strings.Contains(body, "hunter2") {
		t.Fatal("password rendered")
	}
	// A blank password on re-save keeps the stored one.
	form.Del("password")
	redirectTo(t, post(t, ts, h.app, base+"/broker", form, adminSess), base+"/settings/mqtt")
	if b2, _ := st.GetOrgBroker(ctx, org.ID); string(b2.PasswordSealed) != string(b.PasswordSealed) {
		t.Fatal("blank password cleared the stored one")
	}

	redirectTo(t, post(t, ts, h.app, base+"/broker/delete", nil, adminSess), base+"/settings/mqtt")
	if _, err := st.GetOrgBroker(ctx, org.ID); err == nil {
		t.Fatal("broker not deleted")
	}
}

// TestBrokerProbeRefusesPrivateAddress is the SSRF regression for the "test
// connection" button: even a private address that got into the database (by
// DNS changing after save, say) is refused at dial time.
func TestBrokerProbeRefusesPrivateAddress(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "probeadmin")
	org, err := st.CreateOrg(ctx, "Probe Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	b := store.OrgBroker{OrgID: org.ID, URL: "tcp://127.0.0.1:1883", TopicPrefix: "meshcore", Enabled: true}
	if err := st.UpsertOrgBroker(ctx, b, nil, false, admin.ID); err != nil {
		t.Fatal(err)
	}
	q := redirectTo(t, post(t, ts, h.app, "/orgs/"+org.Slug+"/broker/test", nil, sess), "/orgs/"+org.Slug+"/settings/mqtt")
	if !strings.Contains(q.Get("error"), "public internet") {
		t.Fatalf("probe error = %q, want the blocked-address message", q.Get("error"))
	}
}

func TestOrgActivityPage(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, adminSess := appLogin(t, ts, st, ctx, h.app, "actadmin")
	_, outsiderSess := appLogin(t, ts, st, ctx, h.app, "actoutsider")
	org, err := st.CreateOrg(ctx, "Act Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	rep := observetest.NewIdentity(t).String()
	if _, err := st.CreateRepeater(ctx, &store.Repeater{OwnerID: admin.ID, Name: "Hilltop", PublicKeyHex: rep}); err != nil {
		t.Fatal(err)
	}
	c, err := st.CreateCompanion(ctx, admin.ID, "Admin T1", observetest.NewIdentity(t).String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCompanion(ctx, admin.ID, "Pending T2", observetest.NewIdentity(t).String()); err != nil {
		t.Fatal(err)
	}
	repHash, _ := hex.DecodeString(rep[:6])
	now := time.Now().Add(time.Second)
	if _, err := st.RecordAdvertHeard(ctx, store.AdvertHeard{CompanionID: c.ID, AdvertTS: uint32(now.Unix()), //nolint:gosec // test clock
		ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: now, Relays: [][]byte{repHash}}); err != nil {
		t.Fatal(err)
	}

	// Member activity is a section of the Check-ins page now; its old address
	// still finds it.
	old := do(t, ts, h.app, "/orgs/"+org.Slug+"/activity", adminSess)
	old.Body.Close()
	if want := "/orgs/" + org.Slug + "/nets#activity"; old.StatusCode != http.StatusMovedPermanently || old.Header.Get("Location") != want {
		t.Errorf("GET /activity = %d → %q, want 301 → %s", old.StatusCode, old.Header.Get("Location"), want)
	}
	body := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/nets", adminSess)
	if !strings.Contains(body, "Admin T1") {
		t.Error("activity lacks the confirmed companion")
	}
	// Activity says who checked in, never where: no repeater the member's
	// adverts went through.
	if strings.Contains(body, "Hilltop") {
		t.Error("activity reveals a repeater that carried the member's adverts")
	}
	if strings.Contains(body, "Pending T2") {
		t.Error("an unconfirmed companion is listed")
	}
	if got := status(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/nets", outsiderSess)); got != http.StatusNotFound {
		t.Errorf("outsider GET = %d", got)
	}
}

// TestObserverStatusBadges: a newly registered observer hasn't been heard yet,
// which is different from one that went quiet. Calling the first "Offline"
// read as a broken setup in the minute before its first message arrived.
func TestObserverStatusBadges(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "badgeadmin")
	org, err := st.CreateOrg(ctx, "Badge Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for _, name := range []string{"Fresh", "Quiet", "Busy"} {
		keys[name] = observetest.NewIdentity(t).String()
		if err := st.CreateOrgObserver(ctx, org.ID, name, keys[name], admin.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.TouchObserver(ctx, org.ID, keys["Quiet"], time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchObserver(ctx, org.ID, keys["Busy"], time.Now()); err != nil {
		t.Fatal(err)
	}

	body := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/settings/mqtt", sess)
	for name, want := range map[string]string{"Fresh": "Not heard yet", "Quiet": "Offline", "Busy": "Online"} {
		// The status badge follows the observer's name, on the same line.
		row := body[strings.Index(body, ">"+name+"<"):]
		row = row[:strings.Index(row, "</div>")]
		if !strings.Contains(row, want) {
			t.Errorf("%s observer row lacks %q: %s", name, want, row)
		}
	}
}

// Settings › MQTT lists approved observers by default, with a filter (and
// counts) for the ones discovered from the broker; admins approve, rename and
// unapprove them.
func TestObserverApproval(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, adminSess := appLogin(t, ts, st, ctx, h.app, "approveadmin")
	member, memberSess := appLogin(t, ts, st, ctx, h.app, "approvemember")
	org, err := st.CreateOrg(ctx, "Approve Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateOrg(ctx, "Approve Other", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	base := "/orgs/" + org.Slug
	roof := observetest.NewIdentity(t).String()
	if err := st.CreateOrgObserver(ctx, org.ID, "Roof", roof, admin.ID); err != nil {
		t.Fatal(err)
	}
	pickle := observetest.NewIdentity(t).String()
	if err := st.ReportObserver(ctx, org.ID, pickle, "SYR", time.Now(), &observe.Status{Name: "Pickle"}); err != nil {
		t.Fatal(err)
	}
	list, _ := st.ListOrgObservers(ctx, org.ID, store.ObserversUnapproved)
	oid := fmt.Sprint(list[0].ID)
	elsewhere := observetest.NewIdentity(t).String()
	_ = st.ReportObserver(ctx, other.ID, elsewhere, "BUF", time.Now(), &observe.Status{Name: "Elsewhere"})
	otherList, _ := st.ListOrgObservers(ctx, other.ID, store.ObserversAll)

	// The filter: Approved by default; each option shows its count.
	shows := func(query string, want, notWant []string) {
		t.Helper()
		body := getBody(t, ts, h.app, base+"/settings/mqtt"+query, adminSess)
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%q: missing %q", query, w)
			}
		}
		for _, w := range notWant {
			if strings.Contains(body, w) {
				t.Errorf("%q: shows %q", query, w)
			}
		}
	}
	shows("", []string{">Roof<", `data-testid="observer-count-approved">1<`, `data-testid="observer-count-unapproved">1<`, `data-testid="observer-count-all">2<`},
		[]string{">Pickle<", "Elsewhere"})
	shows("?observers=unapproved", []string{">Pickle<", "SYR"}, []string{">Roof<"})
	shows("?observers=all", []string{">Pickle<", ">Roof<"}, []string{"Elsewhere"})
	shows("?observers=bogus", []string{">Roof<"}, []string{">Pickle<"})

	// Only admins see the approve page or act on observers.
	approvePage := base + "/settings/mqtt/observers/" + oid
	if got := status(t, do(t, ts, h.app, approvePage, memberSess)); got != http.StatusNotFound {
		t.Errorf("member GET approve page = %d", got)
	}
	for _, p := range []string{"", "/unapprove", "/delete"} {
		if got := status(t, post(t, ts, h.app, base+"/observers/"+oid+p, url.Values{"name": {"x"}}, memberSess)); got != http.StatusNotFound {
			t.Errorf("member POST %q = %d", p, got)
		}
	}
	// Another org's observer isn't reachable through this org.
	foreign := fmt.Sprint(otherList[0].ID)
	if got := status(t, do(t, ts, h.app, base+"/settings/mqtt/observers/"+foreign, adminSess)); got != http.StatusNotFound {
		t.Errorf("foreign approve page = %d", got)
	}
	if got := status(t, post(t, ts, h.app, base+"/observers/"+foreign, url.Values{"name": {"Mine"}}, adminSess)); got != http.StatusNotFound {
		t.Errorf("foreign approve = %d", got)
	}

	// The approve page offers the reported name; a blank one is refused.
	if body := getBody(t, ts, h.app, approvePage, adminSess); !strings.Contains(body, `value="Pickle"`) || !strings.Contains(body, pickle) {
		t.Error("approve page lacks the reported name or key")
	}
	if q := redirectTo(t, post(t, ts, h.app, base+"/observers/"+oid, url.Values{"name": {"  "}}, adminSess), approvePage); q.Get("error") == "" {
		t.Error("blank name accepted")
	}
	q := redirectTo(t, post(t, ts, h.app, base+"/observers/"+oid, url.Values{"name": {"Pickle's roof"}, "from": {"unapproved"}}, adminSess), base+"/settings/mqtt")
	if q.Get("observers") != "unapproved" {
		t.Errorf("approve returned to %v, want the list it came from", q)
	}
	if ok, _ := st.IsOrgObserver(ctx, org.ID, pickle); !ok {
		t.Fatal("approved observer doesn't count")
	}
	shows("", []string{"Pickle&#39;s roof", `data-testid="observer-count-approved">2<`, `data-testid="observer-count-unapproved">0<`}, nil)

	redirectTo(t, post(t, ts, h.app, base+"/observers/"+oid+"/unapprove", url.Values{"from": {"bogus"}}, adminSess), base+"/settings/mqtt")
	if ok, _ := st.IsOrgObserver(ctx, org.ID, pickle); ok {
		t.Fatal("unapproved observer still counts")
	}
}

// TestCompanionOnPublicProfile: a companion is private until its owner shows it
// (on registering, or later with the switch), and only the owner can.
func TestCompanionOnPublicProfile(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	alice, aliceSess := appLogin(t, ts, st, ctx, h.app, "profilealice")
	_, bobSess := appLogin(t, ts, st, ctx, h.app, "profilebob")

	// Registered with "Show on my public profile" ticked.
	shown := post(t, ts, h.app, "/companions", url.Values{"name": {"Shown T1"}, "public_key": {observetest.NewIdentity(t).String()}, "public": {"1"}}, aliceSess)
	shown.Body.Close()
	// Registered without it.
	hidden := post(t, ts, h.app, "/companions", url.Values{"name": {"Hidden T2"}, "public_key": {observetest.NewIdentity(t).String()}}, aliceSess)
	hidden.Body.Close()
	hiddenPath := hidden.Header.Get("Location")

	public := func() []string {
		t.Helper()
		cs, err := st.ListPublicCompanions(ctx, alice.ID)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, c := range cs {
			names = append(names, c.Name)
		}
		return names
	}
	if got := public(); len(got) != 1 || got[0] != "Shown T1" {
		t.Fatalf("public after registering = %v", got)
	}
	if body := getBody(t, ts, h.app, hiddenPath, aliceSess); !strings.Contains(body, `data-testid="companion-public"`) {
		t.Error("the owner's companion page has no visibility switch")
	}

	// Someone else can't flip it; the owner can, both ways.
	if got := status(t, post(t, ts, h.app, hiddenPath+"/visibility", url.Values{"public": {"1"}}, bobSess)); got != http.StatusNotFound {
		t.Errorf("another user's visibility change = %d, want 404", got)
	}
	if q := redirectTo(t, post(t, ts, h.app, hiddenPath+"/visibility", url.Values{"public": {"1"}}, aliceSess), hiddenPath); q.Get("ok") == "" {
		t.Error("no confirmation after showing it")
	}
	if got := public(); len(got) != 2 {
		t.Fatalf("public after showing = %v", got)
	}
	redirectTo(t, post(t, ts, h.app, hiddenPath+"/visibility", nil, aliceSess), hiddenPath)
	if got := public(); len(got) != 1 {
		t.Fatalf("public after hiding = %v", got)
	}
}
