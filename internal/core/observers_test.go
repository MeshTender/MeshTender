package core

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/observe"
	"github.com/MeshTender/MeshTender/internal/observe/observetest"
)

// TestAddObserverByKey: a pasted key is normalized and added, landing on the
// observer's page; a bad key or a second add of the same one is refused with
// a message back on the form.
func TestAddObserverByKey(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	user, sess := appLogin(t, ts, st, ctx, h.app, "addobserver")
	key := observetest.NewIdentity(t).String()

	resp := post(t, ts, h.app, "/observers", url.Values{"public_key": {"  " + strings.ToUpper(key) + "\n"}}, sess)
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/observers/") {
		t.Fatalf("add = %d → %q, want 303 to the observer's page", resp.StatusCode, loc)
	}
	list, err := st.ListObserversForUser(ctx, user.ID)
	if err != nil || len(list) != 1 || list[0].PublicKeyHex != key {
		t.Fatalf("stored = %v, %v; want the lowercased key", list, err)
	}

	for name, value := range map[string]string{"not a key": "hello", "already added": key} {
		q := redirectTo(t, post(t, ts, h.app, "/observers", url.Values{"public_key": {value}}, sess), "/observers/new")
		if q.Get("error") == "" {
			t.Errorf("%s: no error message", name)
		}
	}
}

// TestObserverPage: the page shows the latest report and the organizations it
// reports to that the user is in, counting the others without naming them.
// Before any broker hears it, it says so. Another member can't open it.
func TestObserverPage(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	user, sess := appLogin(t, ts, st, ctx, h.app, "observerpage")
	_, otherSess := appLogin(t, ts, st, ctx, h.app, "observerother")
	stranger, err := st.CreateUser(ctx, "observerstranger", "")
	if err != nil {
		t.Fatal(err)
	}
	key := observetest.NewIdentity(t).String()
	o, err := st.CreateUserObserver(ctx, user.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	path := "/observers/" + o.PublicID

	quiet := getBody(t, ts, h.app, path, sess)
	checkHeadingOutline(t, "observer page (not heard)", quiet)
	if !strings.Contains(quiet, `data-testid="observer-not-heard"`) || strings.Contains(quiet, `data-testid="observer-orgs"`) {
		t.Error("an observer no broker has heard should say so and list no organizations")
	}

	mine, err := st.CreateOrg(ctx, "My Mesh", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := st.CreateOrg(ctx, "Secret Mesh", stranger.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	battery := 4120
	if err := st.ReportObserver(ctx, theirs.ID, key, "SEA", now.Add(-time.Hour), &observe.Status{Name: "Theirs"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReportObserver(ctx, mine.ID, key, "PDX", now, &observe.Status{Name: "Hilltop", FirmwareVersion: "v1.18.0.4313-meshtender-fe351d9f", Model: "Heltec V4", BatteryMV: &battery}); err != nil {
		t.Fatal(err)
	}

	body := getBody(t, ts, h.app, path, sess)
	checkHeadingOutline(t, "observer page", body)
	for _, want := range []string{"Hilltop", "v1.18.0.4313-meshtender-fe351d9f", "Heltec V4", "PDX", "4120 mV", "My Mesh", "Awaiting approval", `data-testid="observer-other-orgs"`, "1 organization you're not a member of"} {
		if !strings.Contains(body, want) {
			t.Errorf("observer page lacks %q", want)
		}
	}
	if strings.Contains(body, "Secret Mesh") {
		t.Error("the page names an organization the user isn't in")
	}

	if got := status(t, do(t, ts, h.app, path, otherSess)); got != http.StatusNotFound {
		t.Errorf("another member's GET = %d, want 404", got)
	}
}

func TestRemoveObserver(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	user, sess := appLogin(t, ts, st, ctx, h.app, "removeobserver")
	_, otherSess := appLogin(t, ts, st, ctx, h.app, "removeother")
	o, err := st.CreateUserObserver(ctx, user.ID, observetest.NewIdentity(t).String())
	if err != nil {
		t.Fatal(err)
	}
	path := "/observers/" + o.PublicID + "/delete"
	if got := status(t, post(t, ts, h.app, path, nil, otherSess)); got != http.StatusNotFound {
		t.Errorf("another member's delete = %d, want 404", got)
	}
	redirectTo(t, post(t, ts, h.app, path, nil, sess), "/nodes")
	if list, _ := st.ListObserversForUser(ctx, user.ID); len(list) != 0 {
		t.Error("observer not removed")
	}
}

// TestOldInstallAddressRedirects: the install page's first address leads to
// Add an observer.
func TestOldInstallAddressRedirects(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	_, sess := appLogin(t, ts, st, ctx, h.app, "oldinstall")
	resp := do(t, ts, h.app, "/observers/install", sess)
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/observers/new" {
		t.Errorf("GET /observers/install = %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// TestMQTTSettingsPitchObserverFirmware: an org's MQTT settings offer
// MeshTender's observer firmware in an info box, linking to what it needs from
// the broker (its README) and to installing it.
func TestMQTTSettingsPitchObserverFirmware(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "pitchadmin")
	org, err := st.CreateOrg(ctx, "Pitch Mesh", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, card, ok := strings.Cut(getBody(t, ts, h.app, "/orgs/"+org.Slug+"/settings/mqtt", sess), `class="alert alert-info mb-3" role="note" data-testid="observer-firmware"`)
	if !ok {
		t.Fatal("no observer firmware card")
	}
	card, _, _ = strings.Cut(card, `data-testid="observer-filter"`)
	for _, want := range []string{`href="https://github.com/MeshTender/MeshCore/blob/meshtender/MESHTENDER.md"`, `href="/observers/new"`} {
		if !strings.Contains(card, want) {
			t.Errorf("observer firmware card lacks %s", want)
		}
	}
}
