package core

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/store"
)

// heardRepeater records flood adverts from rep every interval, the last one
// carrying name and location (lat, lon; both 0 for none).
func heardRepeater(t *testing.T, st *store.Store, rep *store.Repeater, name string, lat, lon float64, interval time.Duration) {
	t.Helper()
	at := time.Now().Add(-3 * interval)
	for i := range 3 {
		a := store.RepeaterAdvert{PublicKeyHex: rep.PublicKeyHex, AdvertTS: uint32(at.Unix()), //nolint:gosec // test clock
			ReceivedAt: at, Flood: true, Name: name, PathHashSize: 2, Packet: []byte{0x11}}
		if i == 2 && (lat != 0 || lon != 0) {
			a.Lat, a.Lon = &lat, &lon
		}
		if _, err := st.RecordRepeaterAdvert(t.Context(), a); err != nil {
			t.Fatal(err)
		}
		at = at.Add(interval)
	}
}

func TestRepeaterPageHeard(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "heardowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Hilltop")
	page := "/repeaters/" + rep.PublicID

	body := getBody(t, ts, h.app, page, sess)
	// The grid keeps every item, so its pairs line up either way.
	if !strings.Contains(body, "Path hash size") || strings.Count(body, "Not heard yet") != 4 {
		t.Fatalf("unheard repeater: want its advert details as \"Not heard yet\", got %d", strings.Count(body, "Not heard yet"))
	}

	heardRepeater(t, st, rep, "Hilltop NEW", 42.9, -78.8, 3*time.Hour)
	body = getBody(t, ts, h.app, page, sess)
	// Mismatches are flagged with the advertised value as hover text, and the
	// fix is in Settings, not here.
	for _, want := range []string{
		`data-testid="heard-name-differs"`, "It advertises “Hilltop NEW”. Change it in Settings › Details.",
		`data-testid="heard-location-differs"`, "It advertises 42.9, -78.8.",
		"2-byte", "Unscoped", "Every 3 h", "Old default (before 1.8)", "In sync",
		`data-testid="heard-interval-outdated"`, "set flood.advert.interval 47",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	for _, unwanted := range []string{`data-testid="heard-clock"`, `data-testid="adopt-name"`, `data-testid="adopt-location"`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("overview has %q", unwanted)
		}
	}
}

func TestAdoptHeardValues(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "adoptowner")
	member, memberSess := appLogin(t, ts, st, ctx, h.app, "adoptmember")
	org, err := st.CreateOrg(ctx, "AdoptOrg", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Hilltop")
	page := "/repeaters/" + rep.PublicID
	settings := page + "/settings"

	// Nothing heard yet: nothing to adopt, and Settings offers nothing.
	resp := post(t, ts, h.app, page+"/heard/name", nil, sess)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("adopt unheard name = %d, want 404", resp.StatusCode)
	}
	if body := getBody(t, ts, h.app, settings, sess); strings.Contains(body, `data-testid="adopt-name"`) {
		t.Fatal("Settings offers to adopt from an unheard repeater")
	}

	heardRepeater(t, st, rep, "Hilltop NEW", 42.9, -78.8, 47*time.Hour)

	body := getBody(t, ts, h.app, settings, sess)
	for _, want := range []string{`data-testid="adopt-name"`, "It advertises “Hilltop NEW”.",
		`data-testid="adopt-location"`, "It advertises 42.9, -78.8."} {
		if !strings.Contains(body, want) {
			t.Errorf("Settings lacks %q", want)
		}
	}
	// A non-owner sees the flags on the Overview but can't adopt either value.
	if body := getBody(t, ts, h.app, page, memberSess); !strings.Contains(body, "Its owner can update it.") {
		t.Error("non-owner's hover text points at Settings")
	}
	for _, path := range []string{"/heard/name", "/heard/location"} {
		resp = post(t, ts, h.app, page+path, nil, memberSess)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("member adopt %s = %d, want 404", path, resp.StatusCode)
		}
	}

	// The values come from what was heard, whatever the form says.
	resp = post(t, ts, h.app, page+"/heard/location", url.Values{"lat": {"1"}, "lon": {"1"}}, sess)
	resp.Body.Close()
	assertRedirect(t, resp, settings, "adopt location")
	resp = post(t, ts, h.app, page+"/heard/name", url.Values{"name": {"Forged"}}, sess)
	resp.Body.Close()
	assertRedirect(t, resp, settings, "adopt name")
	got, err := st.GetRepeaterForUser(ctx, owner.ID, rep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Hilltop NEW" || got.Latitude == nil || *got.Latitude != 42.9 || *got.Longitude != -78.8 {
		t.Fatalf("repeater = %q %v,%v, want the advertised name and location", got.Name, got.Latitude, got.Longitude)
	}

	// Once adopted, nothing differs, and the current default isn't flagged.
	body = getBody(t, ts, h.app, page, sess)
	for _, gone := range []string{`data-testid="heard-name-differs"`, `data-testid="heard-location-differs"`, `data-testid="heard-interval-outdated"`} {
		if strings.Contains(body, gone) {
			t.Errorf("after adopting, page still has %q", gone)
		}
	}
	if !strings.Contains(body, "Current default") {
		t.Error("47 h not shown as the current default")
	}

	// The org list shows it too, for members.
	if list := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/repeaters", memberSess); !strings.Contains(list, `data-testid="org-rep-interval"`) || !strings.Contains(list, "Every 47 h") {
		t.Error("org repeater list lacks the flood advert interval")
	}
}
