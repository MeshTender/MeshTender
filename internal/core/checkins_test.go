package core

import (
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

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
		q := redirectTo(t, post(t, ts, h.app, "/companions", form, bobSess), "/companions")
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

func TestCompanionPageResolvesRoutes(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	alice, sess := appLogin(t, ts, st, ctx, h.app, "routealice")
	if _, err := st.CreateOrg(ctx, "Route Org", alice.ID); err != nil {
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
	now := time.Now()
	repHash, _ := hex.DecodeString(rep[:6])
	for _, sg := range []store.AdvertSighting{
		{CompanionID: c.ID, ObserverPublicKeyHex: strings.Repeat("ab", 32), ReceivedAt: now, AdvertTS: uint32(now.Unix()), //nolint:gosec // test clock
			RouteType: 1, PathHashSize: 3, Path: append(repHash, 0x99, 0x99, 0x99)},
		{CompanionID: c.ID, ObserverPublicKeyHex: strings.Repeat("cd", 32), ReceivedAt: now, AdvertTS: uint32(now.Unix()), //nolint:gosec // test clock
			RouteType: 2, PathHashSize: 1, Path: []byte{}},
	} {
		if _, err := st.RecordAdvertSighting(ctx, sg); err != nil {
			t.Fatal(err)
		}
	}
	body := getBody(t, ts, h.app, "/companions/"+c.PublicID, sess)
	for _, want := range []string{"Hilltop", "999999", "Zero-hop", "Flood"} {
		if !strings.Contains(body, want) {
			t.Errorf("companion page lacks %q", want)
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

	// Members read; outsiders don't; only admins write.
	if body := getBody(t, ts, h.app, base+"/observers", memberSess); strings.Contains(body, `action="`+base+`/broker"`) {
		t.Error("a member sees the broker form")
	}
	if got := status(t, do(t, ts, h.app, base+"/observers", outsiderSess)); got != http.StatusNotFound {
		t.Errorf("outsider GET = %d", got)
	}
	for _, p := range []string{"/observers", "/broker", "/broker/test", "/broker/delete"} {
		form := url.Values{"name": {"x"}, "public_key": {obsKey}, "url": {"tcp://203.0.113.10"}}
		if got := status(t, post(t, ts, h.app, base+p, form, memberSess)); got != http.StatusNotFound {
			t.Errorf("member POST %s = %d, want 404", p, got)
		}
	}

	redirectTo(t, post(t, ts, h.app, base+"/observers", url.Values{"name": {"Roof"}, "public_key": {strings.ToUpper(obsKey)}}, adminSess), base+"/observers")
	if ok, _ := st.IsOrgObserver(ctx, org.ID, obsKey); !ok {
		t.Fatal("observer not registered under its lowercase key")
	}
	if q := redirectTo(t, post(t, ts, h.app, base+"/observers", url.Values{"name": {"Roof"}, "public_key": {obsKey}}, adminSess), base+"/observers"); q.Get("error") == "" {
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
		q := redirectTo(t, post(t, ts, h.app, base+"/broker", form, adminSess), base+"/observers")
		if q.Get("error") == "" {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := st.GetOrgBroker(ctx, org.ID); err == nil {
		t.Fatal("a refused broker was saved")
	}

	form := url.Values{"url": {"tls://203.0.113.10:8883"}, "username": {"meshtender"}, "password": {"hunter2"}, "enabled": {"1"}}
	redirectTo(t, post(t, ts, h.app, base+"/broker", form, adminSess), base+"/observers")
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
	if body := getBody(t, ts, h.app, base+"/observers", adminSess); strings.Contains(body, "hunter2") {
		t.Fatal("password rendered")
	}
	// A blank password on re-save keeps the stored one.
	form.Del("password")
	redirectTo(t, post(t, ts, h.app, base+"/broker", form, adminSess), base+"/observers")
	if b2, _ := st.GetOrgBroker(ctx, org.ID); string(b2.PasswordSealed) != string(b.PasswordSealed) {
		t.Fatal("blank password cleared the stored one")
	}

	redirectTo(t, post(t, ts, h.app, base+"/broker/delete", nil, adminSess), base+"/observers")
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
	q := redirectTo(t, post(t, ts, h.app, "/orgs/"+org.Slug+"/broker/test", nil, sess), "/orgs/"+org.Slug+"/observers")
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
	if _, err := st.RecordAdvertSighting(ctx, store.AdvertSighting{CompanionID: c.ID, ObserverPublicKeyHex: strings.Repeat("ab", 32),
		ReceivedAt: now, AdvertTS: uint32(now.Unix()), RouteType: 1, PathHashSize: 3, Path: repHash}); err != nil { //nolint:gosec // test clock
		t.Fatal(err)
	}

	body := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/activity", adminSess)
	if !strings.Contains(body, "Admin T1") || !strings.Contains(body, "Hilltop") {
		t.Error("activity lacks the confirmed companion and its home repeater")
	}
	if strings.Contains(body, "Pending T2") {
		t.Error("an unconfirmed companion is listed")
	}
	if got := status(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/activity", outsiderSess)); got != http.StatusNotFound {
		t.Errorf("outsider GET = %d", got)
	}
}
