package core

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/secretbox"
	"github.com/MeshTender/MeshTender/internal/store"
)

// TestNetAirSettings: an admin sets where net control's messages go out. A
// private channel's key is sealed and write-only — kept when the field is left
// blank, never rendered — and members see the summary but not the form.
func TestNetAirSettings(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, adminSess := appLogin(t, ts, st, ctx, h.app, "airadmin")
	member, memberSess := appLogin(t, ts, st, ctx, h.app, "airmember")
	org, err := st.CreateOrg(ctx, "Air Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateOrg(ctx, "Air Other", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	net, err := st.CreateNet(ctx, org.ID, store.NetInput{Name: "Sunday Net", Weekday: time.Sunday, StartMinute: 19 * 60,
		DurationMinutes: 60, Timezone: "UTC", Active: true}, admin.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	region, err := st.CreateRegion(ctx, org.ID, store.RegionInput{Token: "us-ny", DisplayName: "New York", AllowFlood: true})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := st.CreateRegion(ctx, other.ID, store.RegionInput{Token: "us-ca", DisplayName: "California", AllowFlood: true})
	if err != nil {
		t.Fatal(err)
	}
	netPath := "/orgs/" + org.Slug + "/nets/" + strconv.FormatInt(net.ID, 10)
	save := func(sess *http.Cookie, form url.Values) *http.Response {
		t.Helper()
		resp := post(t, ts, h.app, netPath+"/air", form, sess)
		resp.Body.Close()
		return resp
	}
	air := func() *store.NetAir {
		t.Helper()
		a, err := st.GetNetAir(ctx, org.ID, net.ID)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	page := readBody(t, do(t, ts, h.app, netPath, adminSess))
	if !strings.Contains(page, `data-testid="net-air-no-radio"`) || !strings.Contains(page, "net-air-save") {
		t.Fatal("admin's net page should show the on-air form and the missing org radio")
	}

	// A member can't change them.
	if r := save(memberSess, url.Values{"channel_kind": {"hashtag"}, "channel_name": {"x"}}); r.StatusCode != http.StatusNotFound {
		t.Fatalf("member save = %d", r.StatusCode)
	}

	// Rejected: another org's region, a colon in the sender name, a hashtag with no name.
	for _, form := range []url.Values{
		{"channel_kind": {"public"}, "scope_region": {strconv.FormatInt(foreign, 10)}},
		{"channel_kind": {"public"}, "on_air_name": {"NC: Bob"}},
		{"channel_kind": {"hashtag"}, "channel_name": {"#"}},
		{"channel_kind": {"private"}, "channel_name": {"Ops"}}, // no key stored yet
		{"channel_kind": {"private"}, "channel_key": {"not a key"}},
	} {
		if r := save(adminSess, form); r.StatusCode != http.StatusSeeOther || !strings.Contains(r.Header.Get("Location"), "error=") {
			t.Errorf("%v: status %d, Location %q", form, r.StatusCode, r.Header.Get("Location"))
		}
	}
	if a := air(); a.ChannelKind != store.ChannelPublic || a.OnAirName != "" || a.ScopeRegionID != nil {
		t.Fatalf("a rejected form changed the settings: %+v", a)
	}

	save(adminSess, url.Values{"channel_kind": {"hashtag"}, "channel_name": {"#WNYnet"},
		"scope_region": {strconv.FormatInt(region, 10)}, "on_air_name": {"Net Control"}, "log_prefix": {"news:"}})
	if a := air(); a.ChannelName != "WNYnet" || a.ScopeToken != "us-ny" || a.OnAirName != "Net Control" {
		t.Fatalf("hashtag settings = %+v", a)
	}

	// A private channel: the key is sealed, and leaving the field blank keeps it.
	key := "00112233445566778899aabbccddeeff"
	save(adminSess, url.Values{"channel_kind": {"private"}, "channel_name": {"Ops"}, "channel_key": {key}})
	sealed := air().PSKSealed
	want, _ := hex.DecodeString(key)
	if pt, err := secretbox.Open(testMasterKey, sealed); err != nil || !bytes.Equal(pt, want) {
		t.Fatalf("stored key opens to %x, %v", pt, err)
	}
	save(adminSess, url.Values{"channel_kind": {"private"}, "channel_name": {"Ops 2"}})
	if a := air(); !bytes.Equal(a.PSKSealed, sealed) || a.ChannelName != "Ops 2" {
		t.Fatalf("resave without a key = %+v", a)
	}

	for _, sess := range []*http.Cookie{adminSess, memberSess} {
		page := readBody(t, do(t, ts, h.app, netPath, sess))
		if strings.Contains(page, key) || strings.Contains(page, hex.EncodeToString(sealed)) {
			t.Fatal("the net page leaks the channel key")
		}
		if !strings.Contains(page, "Ops 2") || !strings.Contains(page, `data-testid="net-air-summary"`) {
			t.Error("net page missing the on-air summary")
		}
	}
	if page := readBody(t, do(t, ts, h.app, netPath, memberSess)); strings.Contains(page, "net-air-save") {
		t.Error("a member sees the on-air form")
	}
}
