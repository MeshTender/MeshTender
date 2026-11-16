package core

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestOrgGuideGetOnTheAir: the Guide's "Get on the air" page tells anyone —
// member, signed-in non-member or anonymous visitor — the preset and values to
// put on their companion, and that those alone put them on the mesh; that
// registering it (on the app host) is optional and only makes adverts count as
// check-ins; and the nets to check in to — under a Guide tab with its rail.
func TestOrgGuideGetOnTheAir(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, adminSess := appLogin(t, ts, st, ctx, h.app, "guideadmin")
	_, visitorSess := appLogin(t, ts, st, ctx, h.app, "guidevisitor")
	org, err := st.CreateOrg(ctx, "Guide Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	radio := defaultRadio()
	if err := st.SetOrgRadio(ctx, org.ID, &radio); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	weekly := netAround("Sunday Check-in", now.Add(2*time.Hour))
	if _, err := st.CreateNet(ctx, org.ID, weekly, admin.ID, now); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{
		"member (app host)":     getBody(t, ts, h.app, "/orgs/"+org.Slug+"/guide", adminSess),
		"non-member (app host)": getBody(t, ts, h.app, "/orgs/"+org.Slug+"/guide", visitorSess),
		"anonymous (root host)": getBody(t, ts, h.root, "/orgs/"+org.Slug+"/guide"),
	} {
		for _, want := range []string{
			`class="nav-link active" href="/orgs/` + org.Slug + `/guide" data-testid="org-tab-guide"`,
			`data-testid="org-guide-nav"`,
			"Pick the <strong>" + radio.PresetName() + "</strong> preset",
			`data-testid="guide-on-the-mesh"`, "you should be able to send and receive messages",
			`data-testid="guide-register-optional"`, "need to register to use the mesh",
			radio.MHz() + " MHz", radio.KHz() + " kHz",
			`/companions/new" data-testid="guide-register-link"`,
			"Sunday Check-in", weekly.Schedule(),
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: Get on the air lacks %q", name, want)
			}
		}
	}
}

// TestOrgGuideWithoutRadio: until an admin sets the radio, the Guide says so —
// pointing an admin at Settings and anyone else at an admin — and says no nets
// are scheduled rather than showing an empty list.
func TestOrgGuideWithoutRadio(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, adminSess := appLogin(t, ts, st, ctx, h.app, "guidenoradio")
	org, err := st.CreateOrg(ctx, "Quiet Guide Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings := `href="/orgs/` + org.Slug + `/settings/radio"`

	adminView := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/guide", adminSess)
	if !strings.Contains(adminView, `data-testid="guide-no-radio"`) || !strings.Contains(adminView, settings) {
		t.Error("admin's Guide doesn't point at Settings for the missing radio")
	}
	anon := getBody(t, ts, h.root, "/orgs/"+org.Slug+"/guide")
	if !strings.Contains(anon, `data-testid="guide-no-radio"`) || strings.Contains(anon, settings) || !strings.Contains(anon, "Ask an admin") {
		t.Error("public Guide without a radio should send visitors to an admin, not Settings")
	}
	if !strings.Contains(anon, "No nets are scheduled yet.") || strings.Contains(anon, `data-testid="guide-nets"`) {
		t.Error("Guide without nets should say so")
	}
}

// TestOrgConfigMovedToGuide: the recommended configuration's old address
// redirects permanently to Guide › Repeater setup on both hosts, keeping the
// chosen profile and preview location, so shared links still open the same view.
func TestOrgConfigMovedToGuide(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "guidemoved")
	org, err := st.CreateOrg(ctx, "Moved Config Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := "/orgs/" + org.Slug + "/guide/repeaters?profile=Solar+node&lat=15&lon=35"
	for name, resp := range map[string]*http.Response{
		"app host":  do(t, ts, h.app, "/orgs/"+org.Slug+"/config?profile=Solar+node&lat=15&lon=35", sess),
		"root host": do(t, ts, h.root, "/orgs/"+org.Slug+"/config?profile=Solar+node&lat=15&lon=35"),
	} {
		resp.Body.Close()
		if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != want {
			t.Errorf("%s: GET /config = %d → %q, want 301 → %q", name, resp.StatusCode, resp.Header.Get("Location"), want)
		}
	}
	page := getBody(t, ts, h.root, "/orgs/"+org.Slug+"/guide/repeaters")
	if !strings.Contains(page, `data-testid="org-guide-repeaters"`) || !strings.Contains(page, `aria-current="page"`) {
		t.Error("Repeater setup isn't the active Guide section")
	}
}
