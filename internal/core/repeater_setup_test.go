package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/jleight/meshtender/internal/store"
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
	save := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles", url.Values{
		"profile_name":  {"Std"},
		"profile_steps": {"set radio 910.525,62.5,7,5\nset tx 22"},
	}, sess)
	save.Body.Close()
	if save.StatusCode != http.StatusSeeOther {
		t.Fatalf("profile save status = %d, want 303", save.StatusCode)
	}
	// Scoped-flooding pattern: allow flood inside the zone, deny at the root. The
	// region itself is fixture here — the region editor's own endpoints are covered
	// in config_region_modal_test.go — so it's written straight to the store.
	if _, err := st.CreateRegion(ctx, org.ID, store.RegionInput{
		Token: "zone", DisplayName: "Zone", Layer: 1, AllowFlood: true,
		GeofenceJSON: []byte(`{"type":"Polygon","coordinates":[[[30,10],[40,10],[40,20],[30,20],[30,10]]]}`),
	}); err != nil {
		t.Fatalf("create region: %v", err)
	}
	if err := st.SetRootAllowFlood(ctx, org.ID, false); err != nil {
		t.Fatalf("deny root flood: %v", err)
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
		"set name Hilltop", "set radio 910.525,62.5,7,5", "region def zone",
		"region denyf *", "region allowf zone", "region save",
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
		"lat": {"15.0"}, "lon": {"35.0"}, "expose_public_page": {"1"},
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
	if !got.ExposePublicPage {
		t.Error("expose_public_page from the setup form was not persisted")
	}
}

// TestBuildSetupCommands locks the from-scratch command order (header → profile
// → region → reboot) and the two region-placement modes: appended when the
// profile has no marker, spliced in place when it does.
func TestBuildSetupCommands(t *testing.T) {
	t.Parallel()
	radio := setupRadio{FreqMHz: 910.525, BwKHz: 62.5, SF: 7, CR: 5}
	lat, lon := 15.0, 35.0
	region := []string{"region def zone", "region denyf *", "region allowf zone", "region save"}
	header := []string{
		"set name Hilltop", "set prv.key <key>",
		"set lat 15.000000", "set lon 35.000000",
		"set radio 910.525,62.5,7,5", "setperm srv 3",
	}

	// No marker: region commands land after all profile steps, before reboot. The
	// comment step is not runnable and is skipped.
	steps := []store.ConfigStep{{CommandLine: "set tx 22"}, {Comment: "note"}}
	got := buildSetupCommands("Hilltop", "set prv.key <key>", "setperm srv 3", &lat, &lon, radio, steps, region)
	want := append(append(append([]string{}, header...), "set tx 22"), append(append([]string{}, region...), "reboot")...)
	if !slices.Equal(got, want) {
		t.Fatalf("no-marker order:\n got %v\nwant %v", got, want)
	}

	// With a marker: region commands splice between the surrounding steps.
	steps = []store.ConfigStep{{CommandLine: "set tx 22"}, {CommandLine: store.RegionMarker}, {CommandLine: "set repeat on"}}
	got = buildSetupCommands("Hilltop", "set prv.key <key>", "setperm srv 3", &lat, &lon, radio, steps, region)
	want = append(append([]string{}, header...), "set tx 22")
	want = append(want, region...)
	want = append(want, "set repeat on", "reboot")
	if !slices.Equal(got, want) {
		t.Fatalf("marker-splice order:\n got %v\nwant %v", got, want)
	}

	// No location + empty region (standalone): header skips lat/lon and nothing is
	// spliced.
	got = buildSetupCommands("Solo", "set prv.key <key>", "setperm srv 3", nil, nil, radio, nil, nil)
	want = []string{"set name Solo", "set prv.key <key>", "set radio 910.525,62.5,7,5", "setperm srv 3", "reboot"}
	if !slices.Equal(got, want) {
		t.Fatalf("standalone order:\n got %v\nwant %v", got, want)
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
