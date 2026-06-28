package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// jsonPost issues a JSON POST with an explicit Host header and cookies.
func jsonPost(t *testing.T, url, host, body string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatalf("json post: %v", err)
	}
	return resp
}

// TestSerialSetupFlow exercises the from-scratch USB setup endpoints end to end:
// an org config (profile with a radio step + a geofenced region) drives the
// generated command list, then the repeater record is created — without the
// private key ever being sent to the server.
func TestSerialSetupFlow(t *testing.T) {
	st, ctx, ts, h := splitServer(t)

	// Authenticated admin session (the config_flow_test pattern).
	admin, _ := st.CreateUser(ctx, "setupadmin", "")
	loginID, _ := st.CreateLogin(ctx, admin.ID)
	code, _ := st.CreateAuthCode(ctx, admin.ID, loginID, "/")
	cb := do(t, ts, h.app, "/session/callback?code="+code+"&state=s1", &http.Cookie{Name: "mt_state", Value: "s1"})
	cb.Body.Close()
	sess := cookieByName(cb, "meshtender_session")
	if sess == nil {
		t.Fatal("no app session")
	}

	org, err := st.CreateOrg(ctx, "Setup Org", admin.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// A profile whose steps set the radio, plus a geofenced region (lon 30-40,
	// lat 10-20). Steps resolve against the seeded command catalog.
	form := url.Values{
		"profile_name":   {"Std"},
		"profile_steps":  {"set radio 910.525,62.5,7,5\nset tx 22"},
		"region_display": {"Zone"},
		"region_token":   {"zone"},
		"region_layer":   {"1"},
		"region_geojson": {`{"type":"Polygon","coordinates":[[[30,10],[40,10],[40,20],[30,20],[30,10]]]}`},
	}
	save := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/edit", form, sess)
	save.Body.Close()
	if save.StatusCode != http.StatusSeeOther {
		t.Fatalf("config save status = %d, want 303", save.StatusCode)
	}

	// Build the command list for a point inside the region.
	reqBody, _ := json.Marshal(map[string]any{
		"name": "Hilltop", "orgId": org.ID, "profile": "Std", "lat": 15.0, "lon": 35.0,
	})
	resp := jsonPost(t, ts.URL+"/repeaters/setup/commands", h.app, string(reqBody), sess)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("setup/commands status = %d: %s", resp.StatusCode, b)
	}
	var cmdResp struct {
		Commands            []string `json:"commands"`
		IdentityPlaceholder string   `json:"identityPlaceholder"`
		Radio               struct {
			FreqMHz float64 `json:"freqMhz"`
		} `json:"radio"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cmdResp); err != nil {
		t.Fatalf("decode commands: %v", err)
	}
	resp.Body.Close()

	joined := strings.Join(cmdResp.Commands, "\n")
	for _, want := range []string{
		"set name Hilltop", "set radio 910.525,62.5,7,5", "region def zone", "region save",
		"set lat 15.000000", "set lon 35.000000", "reboot",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("command list missing %q\ngot:\n%s", want, joined)
		}
	}
	if cmdResp.IdentityPlaceholder == "" || !strings.Contains(joined, cmdResp.IdentityPlaceholder) {
		t.Errorf("command list missing identity placeholder %q", cmdResp.IdentityPlaceholder)
	}
	hasSetperm := false
	for _, c := range cmdResp.Commands {
		if strings.HasPrefix(c, "setperm ") && strings.HasSuffix(c, " 3") {
			hasSetperm = true
		}
	}
	if !hasSetperm {
		t.Error("command list missing setperm <server> 3")
	}
	if cmdResp.Radio.FreqMHz != 910.525 {
		t.Errorf("echoed radio freq = %v, want 910.525", cmdResp.Radio.FreqMHz)
	}

	// Create the record (only the public key is sent — never the private key).
	pub := strings.Repeat("ab", 32) // 64 hex chars
	complete := url.Values{
		"name": {"Hilltop"}, "public_key": {pub},
		"radio_freq_mhz": {"910.525"}, "radio_bw_khz": {"62.5"}, "radio_sf": {"7"}, "radio_cr": {"5"},
		"lat": {"15.0"}, "lon": {"35.0"},
	}
	cResp := post(t, ts, h.app, "/repeaters/setup/complete", complete, sess)
	if cResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(cResp.Body)
		cResp.Body.Close()
		t.Fatalf("setup/complete status = %d: %s", cResp.StatusCode, b)
	}
	var done struct {
		Redirect string `json:"redirect"`
	}
	json.NewDecoder(cResp.Body).Decode(&done)
	cResp.Body.Close()
	if !strings.Contains(done.Redirect, "/added") {
		t.Errorf("redirect = %q, want .../added", done.Redirect)
	}

	// The repeater exists, owned by the admin, with the picked location stored.
	reps, err := st.ListRepeatersForUser(ctx, admin.ID)
	if err != nil || len(reps) != 1 {
		t.Fatalf("expected 1 repeater, got %d (err %v)", len(reps), err)
	}
	got := reps[0]
	if got.Name != "Hilltop" || got.PublicKeyHex != pub {
		t.Errorf("repeater = %+v, want name Hilltop / pub %s", got, pub)
	}
	if got.Latitude == nil || got.Longitude == nil || *got.Latitude != 15.0 || *got.Longitude != 35.0 {
		t.Errorf("location not stored: %v, %v", got.Latitude, got.Longitude)
	}
}

func TestParseProfileRadio(t *testing.T) {
	steps := []string{"set tx 22", "set radio 910.525,62.5,7,5", "set repeat on"}
	rad, ok := parseProfileRadio(steps)
	if !ok {
		t.Fatal("expected to find a radio step")
	}
	if rad.FreqMHz != 910.525 || rad.BwKHz != 62.5 || rad.SF != 7 || rad.CR != 5 {
		t.Fatalf("parsed wrong radio: %+v", rad)
	}

	if _, ok := parseProfileRadio([]string{"set tx 22", "set name x"}); ok {
		t.Fatal("expected no radio step")
	}
	// Malformed radio lines are ignored, not partially parsed.
	if _, ok := parseProfileRadio([]string{"set radio 910.5,62.5,7"}); ok {
		t.Fatal("expected malformed radio (3 fields) to be rejected")
	}
}

func TestRadioCommand(t *testing.T) {
	got := radioCommand(setupRadio{FreqMHz: 910.525, BwKHz: 62.5, SF: 7, CR: 5})
	want := "set radio 910.525,62.5,7,5"
	if got != want {
		t.Fatalf("radioCommand = %q, want %q", got, want)
	}
}
