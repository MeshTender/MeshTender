package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/config"
	"github.com/MeshTender/MeshTender/internal/store"
)

var uscaRadio = config.RadioDefaults{FreqHz: 910_525_000, BwHz: 62_500, SF: 7, CR: 5}

// TestOrgRadioSettings: an admin sets the org's radio in Settings › Profile (the
// firmware's ranges enforced), the Configuration page shows it, a profile can
// then reference it through placeholders and is shown filled in, and the radio
// can't be cleared while that profile uses it. Members can't edit it.
func TestOrgRadioSettings(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "radioadmin")
	org, err := st.CreateOrg(ctx, "Radio Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	base := "/orgs/" + org.Slug + "/config"
	settings := "/orgs/" + org.Slug + "/settings"

	// Settings › Profile has the radio form, opening on the default preset.
	page := readBody(t, do(t, ts, h.app, settings, sess))
	if !strings.Contains(page, `data-testid="settings-radio"`) || !strings.Contains(page, "not set yet") ||
		!strings.Contains(page, `selected>USA/Canada`) || strings.Contains(page, "clear-radio") {
		t.Fatal("Settings › Profile should offer to set the radio")
	}
	// A profile can't reference a radio the org hasn't set.
	bad := post(t, ts, h.app, base+"/profiles",
		url.Values{"profile_name": {"Std"}, "profile_steps": {"set radio {{radio.freq}},{{radio.bw}},{{radio.sf}},{{radio.cr}}"}}, sess)
	if body := readBody(t, bad); bad.StatusCode != http.StatusOK || !strings.Contains(body, "set them under Radio first") {
		t.Fatalf("placeholder profile saved without an org radio: %q", body)
	}

	// Out of the firmware's range: the page again, with the error and the entry.
	form := url.Values{"radio_freq_mhz": {"50"}, "radio_bw_khz": {"62.5"}, "radio_sf": {"7"}, "radio_cr": {"5"}}
	resp := post(t, ts, h.app, base+"/radio", form, sess)
	if body := readBody(t, resp); resp.StatusCode != http.StatusOK || !strings.Contains(body, "150–2500 MHz") ||
		!strings.Contains(body, `value="50"`) {
		t.Fatalf("out-of-range radio not re-shown with the error: %q", body)
	}
	if r, _ := st.OrgRadio(ctx, org.ID); r != nil {
		t.Fatalf("out-of-range radio stored: %v", r)
	}

	form.Set("radio_freq_mhz", "910.525")
	resp = post(t, ts, h.app, base+"/radio", form, sess)
	resp.Body.Close()
	if loc, _ := url.Parse(resp.Header.Get("Location")); resp.StatusCode != http.StatusSeeOther || loc.Path != settings || loc.Query().Get("ok") == "" {
		t.Fatalf("save = %d %q, want 303 → %s?ok=…", resp.StatusCode, resp.Header.Get("Location"), settings)
	}
	if r, err := st.OrgRadio(ctx, org.ID); err != nil || r == nil || *r != uscaRadio {
		t.Fatalf("stored radio = %v, %v", r, err)
	}

	// Now the placeholder profile saves, canonical, and the page shows it filled in.
	ok := post(t, ts, h.app, base+"/profiles",
		url.Values{"profile_name": {"Std"}, "profile_steps": {"set radio {{radio.freq}},{{radio.bw}},{{radio.sf}},{{radio.cr}}"}}, sess)
	ok.Body.Close()
	if ok.StatusCode != http.StatusSeeOther {
		t.Fatal("placeholder profile not saved once the radio was set")
	}
	page = readBody(t, do(t, ts, h.app, base, sess))
	for _, want := range []string{"910.525 MHz · BW62.5 · SF7 · CR5", "set radio 910.525,62.5,7,5"} {
		if !strings.Contains(page, want) {
			t.Errorf("config page missing %q", want)
		}
	}
	if strings.Contains(page, "config-radio-missing") {
		t.Error("config page warns about a missing radio that's set")
	}

	// Clearing is refused while the profile uses it.
	clear := post(t, ts, h.app, base+"/radio", url.Values{"clear": {"1"}}, sess)
	if body := readBody(t, clear); clear.StatusCode != http.StatusOK || !strings.Contains(body, "A profile uses these settings") {
		t.Fatalf("clear while in use: %q", body)
	}

	// A plain member sees the radio but can't change it.
	member, memberSess := appLogin(t, ts, st, ctx, h.app, "radiomember")
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	form.Set("radio_freq_mhz", "869.525")
	r := post(t, ts, h.app, base+"/radio", form, memberSess)
	r.Body.Close()
	if got, _ := st.OrgRadio(ctx, org.ID); got == nil || *got != uscaRadio {
		t.Fatalf("member changed the radio: %v", got)
	}
	if page := readBody(t, do(t, ts, h.app, base, memberSess)); !strings.Contains(page, "910.525 MHz") || strings.Contains(page, "settings-radio") {
		t.Error("member view of the radio card")
	}
}

// TestOrgRadioFillsAssembledCommands: the console's apply-configuration list and
// serial setup both get the profile with the org's radio filled in; serial setup
// also uses the org's radio when the profile sets none; and a profile left
// referencing an unset radio is shown but never runnable.
func TestOrgRadioFillsAssembledCommands(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "radioassemble")
	org, err := st.CreateOrg(ctx, "Assemble Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: owner.ID, Name: "R", PublicKeyHex: strings.Repeat("c", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Written straight to the store: the editor would refuse a placeholder
	// before the radio is set, but a concurrent clear could still leave one.
	if _, err := st.CreateProfile(ctx, org.ID, "Std", []store.ConfigStep{
		{CommandLine: "set radio {{ radio.freq }},{{ radio.bw }},{{ radio.sf }},{{ radio.cr }}"},
		{CommandLine: "set tx 22"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProfile(ctx, org.ID, "NoRadio", []store.ConfigStep{{CommandLine: "set tx 22"}}); err != nil {
		t.Fatal(err)
	}
	configJSON := "/repeaters/" + rep.PublicID + "/config.json?profile=Std"
	setup := func(profile string) *http.Response {
		body, _ := json.Marshal(map[string]any{"name": "Hilltop", "orgId": org.ID, "profile": profile})
		return jsonPost(t, ts.URL+"/repeaters/setup/commands", h.app, string(body), sess)
	}

	// No radio yet: shown as written, not runnable, and setup refuses.
	cc := getConsoleConfig(t, ts, h.app, configJSON, sess)
	if len(cc.Commands) == 0 || cc.Commands[0].Runnable || !strings.Contains(cc.Commands[0].Reason, "radio settings aren't set") {
		t.Fatalf("unfilled line = %+v", cc.Commands)
	}
	if page := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config?profile=Std", sess)); !strings.Contains(page, "config-radio-missing") {
		t.Error("config page doesn't warn about the unfilled lines")
	}
	if r := setup("Std"); r.StatusCode != http.StatusConflict {
		r.Body.Close()
		t.Fatalf("setup with an unfilled profile = %d, want 409", r.StatusCode)
	}

	if err := st.SetOrgRadio(ctx, org.ID, &uscaRadio); err != nil {
		t.Fatal(err)
	}
	cc = getConsoleConfig(t, ts, h.app, configJSON, sess)
	if c := cc.Commands[0]; c.Line != "set radio 910.525,62.5,7,5" || !c.Runnable {
		t.Fatalf("filled line = %+v", c)
	}

	commands := func(profile string) (cmds []string, freq float64) {
		r := setup(profile)
		defer r.Body.Close()
		if r.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(r.Body)
			t.Fatalf("setup %s = %d: %s", profile, r.StatusCode, b)
		}
		var out struct {
			Commands []string `json:"commands"`
			Radio    struct {
				FreqMHz float64 `json:"freqMhz"`
			} `json:"radio"`
		}
		if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.Commands, out.Radio.FreqMHz
	}
	for _, profile := range []string{"Std", "NoRadio"} {
		cmds, freq := commands(profile)
		if joined := strings.Join(cmds, "\n"); strings.Contains(joined, "{{") || !strings.Contains(joined, "set radio 910.525,62.5,7,5") || freq != 910.525 {
			t.Errorf("%s setup: radio %v, commands:\n%s", profile, freq, joined)
		}
	}
}

// TestParseRadioRanges: the shared radio form parser accepts exactly what the
// firmware's `set radio` does.
func TestParseRadioRanges(t *testing.T) {
	t.Parallel()
	cases := []struct {
		freq, bw, sf, cr string
		ok               bool
	}{
		{"910.525", "62.5", "7", "5", true},
		{"150", "7", "5", "5", true},
		{"2500", "500", "12", "8", true},
		{"149.9", "62.5", "7", "5", false},
		{"2500.1", "62.5", "7", "5", false},
		{"910.525", "6.9", "7", "5", false},
		{"910.525", "500.1", "7", "5", false},
		{"910.525", "62.5", "4", "5", false},
		{"910.525", "62.5", "13", "5", false},
		{"910.525", "62.5", "7", "9", false},
		{"1e300", "62.5", "7", "5", false},
		{"NaN", "62.5", "7", "5", false},
		{"910.525", "62.5", "-1", "5", false},
		{"", "62.5", "7", "5", false},
	}
	for _, c := range cases {
		r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(url.Values{
			"radio_freq_mhz": {c.freq}, "radio_bw_khz": {c.bw}, "radio_sf": {c.sf}, "radio_cr": {c.cr},
		}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if _, ok := parseRadio(r); ok != c.ok {
			t.Errorf("parseRadio(%s, %s, %s, %s) ok = %v, want %v", c.freq, c.bw, c.sf, c.cr, ok, c.ok)
		}
	}
}
