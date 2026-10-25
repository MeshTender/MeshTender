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

// TestNetAirSettings: an admin sets where net control's messages go out, in
// Settings › Nets. A private channel's key is sealed and write-only — kept when
// the field is left blank, never rendered — and members see the summary on the
// net's page but can't reach the settings.
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
	settingsPath := netSettingsURL(org.Slug, net.ID)
	save := func(sess *http.Cookie, form url.Values) (int, string) {
		t.Helper()
		resp := post(t, ts, h.app, netPath+"/air", form, sess)
		return resp.StatusCode, readBody(t, resp)
	}
	air := func() *store.NetAir {
		t.Helper()
		a, err := st.GetNetAir(ctx, org.ID, net.ID)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	page := readBody(t, do(t, ts, h.app, settingsPath, adminSess))
	if !strings.Contains(page, `data-testid="net-air-no-radio"`) || !strings.Contains(page, "net-air-save") {
		t.Fatal("the net's settings should show the on-air form and the missing org radio")
	}

	// A member can't see or change them.
	if got := status(t, do(t, ts, h.app, settingsPath, memberSess)); got != http.StatusNotFound {
		t.Fatalf("member settings GET = %d", got)
	}
	if code, _ := save(memberSess, url.Values{"channel_kind": {"hashtag"}, "hashtag_name": {"x"}}); code != http.StatusNotFound {
		t.Fatalf("member save = %d", code)
	}

	// Rejected — another org's region, a colon in the sender name, a hashtag
	// with no name, a private channel without a key — re-shows the form with the
	// error and what was entered.
	for _, form := range []url.Values{
		{"channel_kind": {"public"}, "scope_region": {strconv.FormatInt(foreign, 10)}},
		{"channel_kind": {"public"}, "on_air_name": {"NC: Bob"}},
		{"channel_kind": {"hashtag"}, "hashtag_name": {"#"}},
		{"channel_kind": {"private"}, "private_label": {"Ops"}}, // no key stored yet
		{"channel_kind": {"private"}, "channel_key": {"not a key"}},
	} {
		code, body := save(adminSess, form)
		if code != http.StatusOK || !strings.Contains(body, `data-testid="settings-form-error"`) {
			t.Errorf("%v: status %d, no error shown", form, code)
		}
		if v := form.Get("on_air_name"); v != "" && !strings.Contains(body, `value="NC: Bob"`) {
			t.Errorf("%v: entered sender not re-shown", form)
		}
	}
	if a := air(); a.ChannelKind != store.ChannelPublic || a.OnAirName != "" || a.ScopeRegionID != nil {
		t.Fatalf("a rejected form changed the settings: %+v", a)
	}

	save(adminSess, url.Values{"channel_kind": {"hashtag"}, "hashtag_name": {"#WNYnet"}, "private_label": {"ignored"},
		"scope_region": {strconv.FormatInt(region, 10)}, "on_air_name": {"Net Control"}, "log_prefix": {"news:"}})
	if a := air(); a.ChannelName != "WNYnet" || a.ScopeToken != "us-ny" || a.OnAirName != "Net Control" {
		t.Fatalf("hashtag settings = %+v", a)
	}
	// Saved as typed, but the uppercase name gets a standing warning.
	if page := readBody(t, do(t, ts, h.app, settingsPath, adminSess)); !strings.Contains(page, `data-testid="net-air-uppercase"`) ||
		!strings.Contains(page, "#wnynet") {
		t.Error("no warning about an uppercase hashtag channel name")
	}
	save(adminSess, url.Values{"channel_kind": {"hashtag"}, "hashtag_name": {"wnynet"}})
	if page := readBody(t, do(t, ts, h.app, settingsPath, adminSess)); strings.Contains(page, `data-testid="net-air-uppercase"`) {
		t.Error("warned about a lowercase hashtag channel name")
	}

	// A private channel: the key is sealed, and leaving the field blank keeps it.
	key := "00112233445566778899aabbccddeeff"
	save(adminSess, url.Values{"channel_kind": {"private"}, "private_label": {"Ops"}, "channel_key": {key}})
	sealed := air().PSKSealed
	want, _ := hex.DecodeString(key)
	if pt, err := secretbox.Open(testMasterKey, sealed); err != nil || !bytes.Equal(pt, want) {
		t.Fatalf("stored key opens to %x, %v", pt, err)
	}
	save(adminSess, url.Values{"channel_kind": {"private"}, "private_label": {"Ops 2"}})
	if a := air(); !bytes.Equal(a.PSKSealed, sealed) || a.ChannelName != "Ops 2" {
		t.Fatalf("resave without a key = %+v", a)
	}

	for _, path := range []string{netPath, settingsPath} {
		page := readBody(t, do(t, ts, h.app, path, adminSess))
		if strings.Contains(page, key) || strings.Contains(page, hex.EncodeToString(sealed)) {
			t.Fatalf("%s leaks the channel key", path)
		}
	}
	page = readBody(t, do(t, ts, h.app, netPath, memberSess))
	if !strings.Contains(page, "Ops 2") || !strings.Contains(page, `data-testid="net-air-summary"`) {
		t.Error("net page missing the on-air summary")
	}
	if strings.Contains(page, "net-air-save") || strings.Contains(page, `href="`+settingsPath+`"`) {
		t.Error("a member sees the on-air form or a link to the net's settings")
	}
}
