package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jleight/meshtender/internal/geo"
	"github.com/jleight/meshtender/internal/store"
)

// cmdIDByKey resolves a catalog command's id by its key (test helper, via the
// public ListCommands API since the test package can't reach the store's pool).
func cmdIDByKey(t *testing.T, st *store.Store, key string) int64 {
	t.Helper()
	cmds, err := st.ListCommands(t.Context())
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	for _, c := range cmds {
		if c.Key == key {
			return c.ID
		}
	}
	t.Fatalf("command %q not in catalog", key)
	return 0
}

// isRegionLine reports whether a recommended-config line came from the region
// commands (every one starts with "region "), used by tests to tell region lines
// apart from profile steps now that the payload carries no per-line kind.
func isRegionLine(line string) bool { return strings.HasPrefix(line, "region ") }

func getConsoleConfig(t *testing.T, ts *httptest.Server, host, path string, cookie *http.Cookie) consoleConfig {
	t.Helper()
	resp := do(t, ts, host, path, cookie)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", path, resp.StatusCode)
	}
	var cc consoleConfig
	if err := json.NewDecoder(resp.Body).Decode(&cc); err != nil {
		t.Fatalf("decode config.json: %v", err)
	}
	return cc
}

// TestConsoleConfigJSON covers the recommended-configuration endpoint: it lists
// the repeater's config-bearing orgs, returns the selected profile's steps plus
// the location-derived region commands, and marks every line runnable or not per
// the caller's permissions (owner: all; plain member: not the admin-tier region
// commands). The whole list is always returned — nothing is hidden.
func TestConsoleConfigJSON(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	owner, ownerCookie := appLogin(t, ts, st, ctx, h.app, "cc-owner")
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: owner.ID, Name: "R", PublicKeyHex: strings.Repeat("e", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "CfgOrg", owner.ID) // owner is an admin member
	if err != nil {
		t.Fatal(err)
	}

	// Ceiling: set.tx is member-tier (everyone in the org can run it); the region
	// write commands stay admin-tier (their catalog default).
	if err := st.UpdateCommandFlags(ctx, cmdIDByKey(t, st, "set.tx"), false, false, true, false); err != nil {
		t.Fatal(err)
	}

	profiles := []store.ProfileInput{{Name: "ESP32", Steps: []store.ConfigStep{
		{CommandLine: "set tx 22"},
		{Comment: "tune antenna later"},
	}}}
	regions := []store.RegionInput{{
		Token: "buf", DisplayName: "Buffalo", Layer: 0, AllowFlood: true,
		GeofenceJSON: geo.Rectangle(40, -80, 45, -75),
	}}
	if err := st.ReplaceOrgConfig(ctx, org.ID, profiles, regions); err != nil {
		t.Fatal(err)
	}

	base := "/repeaters/" + rep.PublicID + "/config.json"

	// Owner, with a preview location inside the Buffalo box.
	cc := getConsoleConfig(t, ts, h.app, base+"?lat=42&lon=-78", ownerCookie)
	if len(cc.Orgs) != 1 || cc.Orgs[0].OrgID != org.ID {
		t.Fatalf("orgs = %+v, want just CfgOrg", cc.Orgs)
	}
	if cc.SelectedProfile != "ESP32" {
		t.Fatalf("selected profile = %q, want ESP32", cc.SelectedProfile)
	}
	if !cc.Location.Known || !cc.Location.RegionsCover {
		t.Fatalf("location = %+v, want known & covered", cc.Location)
	}

	byLine := map[string]consoleConfigCommand{}
	var regionLines, noteCount int
	for _, c := range cc.Commands {
		byLine[c.Line] = c
		if isRegionLine(c.Line) {
			regionLines++
		}
		if c.Line == "" && c.Comment != "" {
			noteCount++
		}
	}
	// Profile command + the note both appear.
	if cmd, ok := byLine["set tx 22"]; !ok || !cmd.Runnable {
		t.Fatalf("set tx 22 = %+v, want present + runnable", cmd)
	}
	if noteCount != 1 {
		t.Fatalf("note count = %d, want 1 (the comment step)", noteCount)
	}
	// Region commands present and runnable for the owner.
	if regionLines == 0 {
		t.Fatal("no region commands emitted for a covered location")
	}
	for _, c := range cc.Commands {
		if isRegionLine(c.Line) && !c.Runnable {
			t.Fatalf("region line %q not runnable for owner", c.Line)
		}
	}

	// A plain member of the org reaches the repeater via org access. They may run
	// the member-tier set.tx but NOT the admin-tier region commands — and every
	// line is still returned, just marked not-runnable.
	member, memberCookie := appLogin(t, ts, st, ctx, h.app, "cc-member")
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	mc := getConsoleConfig(t, ts, h.app, base+"?lat=42&lon=-78", memberCookie)
	var sawRegion bool
	for _, c := range mc.Commands {
		switch {
		case c.Line == "set tx 22" && !c.Runnable:
			t.Error("member should be allowed to run member-tier set tx 22")
		case isRegionLine(c.Line):
			sawRegion = true
			if c.Runnable {
				t.Errorf("member should NOT be allowed to run admin-tier %q", c.Line)
			}
			if c.Reason == "" {
				t.Errorf("region line %q missing a not-runnable reason", c.Line)
			}
		}
	}
	if !sawRegion {
		t.Fatal("member did not receive the region commands (should be shown, just not runnable)")
	}
}

// TestConsoleConfigRegionMarkerInline covers a profile with a {{ region }} marker:
// the region commands are spliced at the marker (between the surrounding steps),
// the marker itself is never emitted as a command.
func TestConsoleConfigRegionMarkerInline(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	owner, ownerCookie := appLogin(t, ts, st, ctx, h.app, "cc-marker")
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: owner.ID, Name: "R", PublicKeyHex: strings.Repeat("d", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "MarkerOrg", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	profiles := []store.ProfileInput{{Name: "P", Steps: []store.ConfigStep{
		{CommandLine: "set tx 22"},
		{CommandLine: store.RegionMarker},
		{CommandLine: "set tx 23"},
	}}}
	regions := []store.RegionInput{{
		Token: "buf", DisplayName: "Buffalo", Layer: 0, AllowFlood: true,
		GeofenceJSON: geo.Rectangle(40, -80, 45, -75),
	}}
	if err := st.ReplaceOrgConfig(ctx, org.ID, profiles, regions); err != nil {
		t.Fatal(err)
	}

	cc := getConsoleConfig(t, ts, h.app, "/repeaters/"+rep.PublicID+"/config.json?lat=42&lon=-78", ownerCookie)

	idx := func(line string) int {
		for i, c := range cc.Commands {
			if c.Line == line {
				return i
			}
		}
		return -1
	}
	pre, post := idx("set tx 22"), idx("set tx 23")
	region := -1
	for i, c := range cc.Commands {
		if isRegionLine(c.Line) {
			region = i
			break
		}
	}
	if pre < 0 || post < 0 || region < 0 {
		t.Fatalf("missing lines: pre=%d region=%d post=%d\n%+v", pre, region, post, cc.Commands)
	}
	if pre >= region || region >= post {
		t.Fatalf("region not spliced between steps: pre=%d region=%d post=%d", pre, region, post)
	}
	if idx(store.RegionMarker) != -1 {
		t.Fatalf("marker leaked into commands:\n%+v", cc.Commands)
	}
}

// TestSetRepeaterLocation covers the map-pick persistence endpoint used when the
// repeater has no location: valid coordinates save and surface via config.json;
// invalid coordinates are rejected.
func TestSetRepeaterLocation(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	owner, cookie := appLogin(t, ts, st, ctx, h.app, "loc-owner")
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: owner.ID, Name: "R", PublicKeyHex: strings.Repeat("f", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Invalid coordinates → 400, nothing stored.
	bad := post(t, ts, h.app, "/repeaters/"+rep.PublicID+"/location",
		url.Values{"lat": {"999"}, "lon": {"0"}}, cookie)
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad coords = %d, want 400", bad.StatusCode)
	}

	// Valid coordinates → 204 and persisted.
	ok := post(t, ts, h.app, "/repeaters/"+rep.PublicID+"/location",
		url.Values{"lat": {"42.5"}, "lon": {"-78.5"}}, cookie)
	ok.Body.Close()
	if ok.StatusCode != http.StatusNoContent {
		t.Fatalf("good coords = %d, want 204", ok.StatusCode)
	}
	got, err := st.GetRepeaterForUser(ctx, owner.ID, rep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Latitude == nil || got.Longitude == nil || *got.Latitude != 42.5 || *got.Longitude != -78.5 {
		t.Fatalf("stored location = %v,%v, want 42.5,-78.5", got.Latitude, got.Longitude)
	}
}
