package core

import (
	"encoding/hex"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/secretbox"
	"github.com/MeshTender/MeshTender/internal/store"
)

var hexKeyRE = regexp.MustCompile(`data-testid="(?:channel|guide-channel)-key">([0-9a-f]{32})<`)

// TestChannelSettings: an admin adds channels on Settings › Channels — a
// "#name" is a hashtag channel, anything else private — and a private channel
// left without a key gets a random one, sealed, which its page then shows; a
// pasted key replaces it; a bad key, a hashtag name with a space and a duplicate
// are refused with what was typed re-shown; an uppercase hashtag name is
// flagged; a plain member can't add one; and a channel can be deleted.
func TestChannelSettings(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "chanadmin")
	member, memberSess := appLogin(t, ts, st, ctx, h.app, "chanmember")
	org, err := st.CreateOrg(ctx, "Chan Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	lake, err := st.CreateRegion(ctx, org.ID, store.RegionInput{Token: "lake", DisplayName: "Lakeshore", Layer: 1})
	if err != nil {
		t.Fatal(err)
	}
	add := func(form url.Values, cookie *http.Cookie) *http.Response {
		return post(t, ts, h.app, "/orgs/"+org.Slug+"/channels", form, cookie)
	}

	for _, form := range []url.Values{
		{"name": {"#lakeshore"}, "scope": {strconv.FormatInt(lake, 10)}, "note": {"Everyday chat."}},
		{"name": {"Ops"}, "scope": {"unscoped"}},
	} {
		resp := add(form, sess)
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("add %s = %d", form.Get("name"), resp.StatusCode)
		}
	}
	list := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/settings/channels", sess)
	if !strings.Contains(list, "#lakeshore") || !strings.Contains(list, "Scope: lake · Everyday chat.") || !strings.Contains(list, "Scope: unscoped") {
		t.Error("the channel list lacks a channel or its scope")
	}

	channels, err := st.ListOrgChannels(ctx, org.ID)
	if err != nil || len(channels) != 2 {
		t.Fatalf("channels = %+v, %v", channels, err)
	}
	ops := channels[1]
	if channels[0].SecretSealed != nil || ops.SecretSealed == nil || ops.Hashtag() {
		t.Fatalf("stored = %+v; want #lakeshore keyless and Ops with a sealed key", channels)
	}
	generated, err := secretbox.Open(testMasterKey, ops.SecretSealed)
	if err != nil || len(generated) != 16 {
		t.Fatalf("generated key = %x, %v", generated, err)
	}
	opsPage := "/orgs/" + org.Slug + "/settings/channels/" + strconv.FormatInt(ops.ID, 10)
	resp := do(t, ts, h.app, opsPage, sess)
	page := readBody(t, resp)
	if m := hexKeyRE.FindStringSubmatch(page); m == nil || m[1] != channelHex(generated) {
		t.Errorf("Ops' page doesn't show its key %x", generated)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("a page with a private key is cacheable: %q", cc)
	}

	const pasted = "00112233445566778899aabbccddeeff"
	upd := post(t, ts, h.app, "/orgs/"+org.Slug+"/channels/"+strconv.FormatInt(ops.ID, 10),
		url.Values{"name": {"Ops"}, "key": {pasted}, "scope": {"default"}}, sess)
	upd.Body.Close()
	if c, _ := st.GetOrgChannel(ctx, org.ID, ops.ID); c == nil || c.ScopeRegionID != nil || c.Unscoped {
		t.Fatalf("after update: %+v; want the default scope", c)
	} else if k, _ := secretbox.Open(testMasterKey, c.SecretSealed); channelHex(k) != pasted {
		t.Errorf("pasted key not stored: %x", k)
	}

	for form, want := range map[string]url.Values{
		"The channel key must be 32 hex digits": {"name": {"Secret"}, "key": {"not-a-key"}},
		"can&#39;t have spaces":                 {"name": {"#two words"}},
		"already has a channel by that name":    {"name": {"#lakeshore"}},
	} {
		body := readBody(t, add(want, sess))
		if !strings.Contains(body, form) || !strings.Contains(body, `value="`+want.Get("name")+`"`) {
			t.Errorf("%v: refusal %q with the entry re-shown is missing", want, form)
		}
	}

	resp = add(url.Values{"name": {"#Lakeside"}}, sess)
	resp.Body.Close()
	upper, _ := st.ListOrgChannels(ctx, org.ID)
	for _, c := range upper {
		if c.Name == "#Lakeside" {
			if body := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/settings/channels/"+strconv.FormatInt(c.ID, 10), sess); !strings.Contains(body, `data-testid="channel-uppercase"`) {
				t.Error("an uppercase hashtag name isn't flagged")
			}
		}
	}

	add(url.Values{"name": {"#members-only"}}, memberSess).Body.Close()
	if all, _ := st.ListOrgChannels(ctx, org.ID); len(all) != 3 {
		t.Errorf("a plain member added a channel: %d channels", len(all))
	}
	post(t, ts, h.app, "/orgs/"+org.Slug+"/channels/"+strconv.FormatInt(ops.ID, 10)+"/delete", nil, sess).Body.Close()
	if _, err := st.GetOrgChannel(ctx, org.ID, ops.ID); err == nil {
		t.Error("the channel wasn't deleted")
	}
}

// TestGuideChannels: the Guide lists the org's channels with their keys, scopes
// and QR codes, and its regions with the default scope marked. A private
// channel — its key and even its name — reaches only members on the app host:
// signed-in non-members and the public root host see only that one exists.
func TestGuideChannels(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "guidechanadmin")
	_, visitor := appLogin(t, ts, st, ctx, h.app, "guidechanvisitor")
	org, err := st.CreateOrg(ctx, "Guide Chan Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRegion(ctx, org.ID, store.RegionInput{Token: "us", DisplayName: "United States", Layer: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRegion(ctx, org.ID, store.RegionInput{Token: "wny", DisplayName: "Western New York", Layer: 2, DefaultScope: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateOrgChannel(ctx, org.ID, store.OrgChannel{Name: "#wny", Note: "Everyday chat."}); err != nil {
		t.Fatal(err)
	}
	const secret = "00112233445566778899aabbccddeeff"
	sealed, err := secretbox.Seal(testMasterKey, mustHex(t, secret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateOrgChannel(ctx, org.ID, store.OrgChannel{Name: "Backroom", SecretSealed: sealed, Unscoped: true}); err != nil {
		t.Fatal(err)
	}
	guide := "/orgs/" + org.Slug + "/guide"

	resp := do(t, ts, h.app, guide, sess)
	memberView := readBody(t, resp)
	for _, want := range []string{
		"#wny", "ad145f5860c081d04dadd28f7a0b3cde", "· your default scope", "Everyday chat.", // the key the MeshCore app shows for #wny
		"Backroom", secret, "· unscoped",
		`alt="QR code that adds this channel in the MeshCore app"`,
		`data-testid="guide-regions"`, "<code>wny</code> Western New York", "Default scope</span>",
		"3 · Register your companion", "4 · Check in during a net",
	} {
		if !strings.Contains(memberView, want) {
			t.Errorf("a member's Guide lacks %q", want)
		}
	}
	assertInOrder(t, "a channel row", memberView, "#wny", `data-testid="guide-channel-key">ad145f5860c081d04dadd28f7a0b3cde<`, `data-testid="guide-channel-note">Everyday chat.<`)
	if strings.Contains(memberView, `data-testid="guide-private-hidden"`) {
		t.Error("a member is told private channels are hidden")
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("a member's Guide, with a private key, is cacheable: %q", cc)
	}

	for name, body := range map[string]string{
		"non-member (app host)": getBody(t, ts, h.app, guide, visitor),
		"anonymous (root host)": getBody(t, ts, h.root, guide),
	} {
		if !strings.Contains(body, "ad145f5860c081d04dadd28f7a0b3cde") || !strings.Contains(body, "Members see 1 private channel once they join.") {
			t.Errorf("%s: lacks the hashtag channel or the private-channel count", name)
		}
		if strings.Contains(body, secret) || strings.Contains(body, "Backroom") {
			t.Errorf("%s: a private channel leaked", name)
		}
	}

	bare, err := st.CreateOrg(ctx, "Bare Chan Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	body := getBody(t, ts, h.root, "/orgs/"+bare.Slug+"/guide")
	if strings.Contains(body, `data-testid="guide-channels"`) || strings.Contains(body, `data-testid="guide-regions"`) ||
		!strings.Contains(body, "2 · Register your companion") || !strings.Contains(body, "3 · Check in during a net") {
		t.Error("a Guide without channels or regions should skip those and number its steps 1–3")
	}
}

func channelHex(b []byte) string { return hex.EncodeToString(b) }

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
