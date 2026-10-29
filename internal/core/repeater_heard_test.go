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
	if !strings.Contains(body, `data-testid="repeater-heard"`) || !strings.Contains(body, "Not heard yet") {
		t.Fatal("unheard repeater: no empty Heard card")
	}

	heardRepeater(t, st, rep, "Hilltop NEW", 42.9, -78.8, 3*time.Hour)
	body = getBody(t, ts, h.app, page, sess)
	for _, want := range []string{
		"Hilltop NEW", `data-testid="heard-name-differs"`, `data-testid="adopt-name"`,
		`data-testid="heard-location-differs"`, "None registered", `data-testid="adopt-location"`,
		"2-byte", "Unscoped", "Every 3 h", "Old default (before 1.8)", "set flood.advert.interval 47",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("heard card lacks %q", want)
		}
	}
	if strings.Contains(body, `data-testid="heard-clock"`) {
		t.Error("in-sync clock flagged")
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

	// Nothing heard yet: nothing to adopt.
	resp := post(t, ts, h.app, page+"/heard/name", nil, sess)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("adopt unheard name = %d, want 404", resp.StatusCode)
	}

	heardRepeater(t, st, rep, "Hilltop NEW", 42.9, -78.8, 47*time.Hour)

	// A co-member can adopt the location but not rename someone else's repeater.
	resp = post(t, ts, h.app, page+"/heard/name", nil, memberSess)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("member adopt name = %d, want 404", resp.StatusCode)
	}
	// The value comes from what was heard, whatever the form says.
	resp = post(t, ts, h.app, page+"/heard/location", url.Values{"lat": {"1"}, "lon": {"1"}}, memberSess)
	resp.Body.Close()
	assertRedirect(t, resp, page, "member adopt location")
	got, err := st.GetRepeaterForUser(ctx, owner.ID, rep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Latitude == nil || *got.Latitude != 42.9 || *got.Longitude != -78.8 {
		t.Fatalf("location = %v,%v, want the advertised one", got.Latitude, got.Longitude)
	}

	resp = post(t, ts, h.app, page+"/heard/name", url.Values{"name": {"Forged"}}, sess)
	resp.Body.Close()
	assertRedirect(t, resp, page, "owner adopt name")
	if got, _ = st.GetRepeaterForUser(ctx, owner.ID, rep.ID); got.Name != "Hilltop NEW" {
		t.Fatalf("name = %q, want the advertised one", got.Name)
	}

	// Once adopted, nothing differs, and the current default isn't flagged.
	body := getBody(t, ts, h.app, page, sess)
	for _, gone := range []string{`data-testid="heard-name-differs"`, `data-testid="heard-location-differs"`, "set flood.advert.interval"} {
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
