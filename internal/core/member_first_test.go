package core

import (
	"crypto/rand"
	"strings"
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/store"
)

// assertInOrder fails unless every marker appears in body, each after the one
// before it.
func assertInOrder(t *testing.T, what, body string, markers ...string) {
	t.Helper()
	at := -1
	for _, m := range markers {
		i := strings.Index(body, m)
		if i < 0 {
			t.Errorf("%s: missing %q", what, m)
			return
		}
		if i < at {
			t.Errorf("%s: %q is out of order, want %q", what, m, markers)
			return
		}
		at = i
	}
}

// TestMemberFeaturesLeadNavigation: taking part (organizations, companions,
// check-ins) comes before repeaters in the top nav, the org tabs and the org
// settings rail. Repeaters are a bonus, not the point of the app.
func TestMemberFeaturesLeadNavigation(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	user, sess := appLogin(t, ts, st, ctx, h.app, "navorder")
	org, err := st.CreateOrg(ctx, "Nav Org", user.ID)
	if err != nil {
		t.Fatal(err)
	}

	dash := getBody(t, ts, h.app, "/", sess)
	_, nav, _ := strings.Cut(dash, `data-testid="topnav"`)
	assertInOrder(t, "top nav", nav, ">Dashboard<", ">Organizations<", ">Companions<", ">Repeaters<", ">Docs<")

	home := getBody(t, ts, h.app, "/orgs/"+org.Slug, sess)
	assertInOrder(t, "org tabs", home,
		`data-testid="org-tab-home"`, `data-testid="org-tab-checkins"`, `data-testid="org-tab-guide"`, `data-testid="org-tab-members"`,
		`data-testid="org-tab-repeaters"`, `data-testid="org-tab-settings"`)

	settings := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/settings", sess)
	assertInOrder(t, "settings rail", settings,
		`data-testid="org-settings-profile"`, `data-testid="org-settings-mqtt"`, `data-testid="org-settings-nets"`,
		`data-testid="org-settings-regions"`, `data-testid="org-settings-profiles"`)
}

// TestRepeaterConfirmationIsOptional: an unconfirmed repeater isn't flagged as
// something to fix. Confirming only verifies the setperm grant and happens on its
// own the first time anyone connects, so the badge is neutral gray rather than a
// warning color, the dashboard keeps no count of them, and the post-add page
// tells owners without a modem they needn't confirm it themselves.
func TestRepeaterConfirmationIsOptional(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	user, sess := appLogin(t, ts, st, ctx, h.app, "unconfirmed")
	id, _ := meshcore.GenerateLocalIdentity(rand.Reader)
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Hilltop", PublicKeyHex: id.String(),
		RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
		ExposePublicPage: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	pages := map[string]string{
		"repeater list":   getBody(t, ts, h.app, "/repeaters", sess),
		"repeater page":   getBody(t, ts, h.app, "/repeaters/"+rep.PublicID, sess),
		"public repeater": getBody(t, ts, h.root, "/r/"+rep.PublicID),
	}
	for name, body := range pages {
		if !strings.Contains(body, `class="badge bg-secondary-lt"`) || !strings.Contains(body, `data-testid="unconfirmed-badge"`) {
			t.Errorf("%s: no neutral Not confirmed badge", name)
		}
		if strings.Contains(body, `bg-orange-lt">Not confirmed`) || strings.Contains(body, `bg-orange-lt" title="No one has reached it`) {
			t.Errorf("%s: Not confirmed is still a warning color", name)
		}
	}

	if dash := getBody(t, ts, h.app, "/", sess); strings.Contains(dash, "not confirmed") {
		t.Error("dashboard still counts unconfirmed repeaters")
	}
	if cfg := getBody(t, ts, h.app, "/repeaters/"+rep.PublicID+"/configure", sess); strings.Contains(cfg, `alert-warning" role="alert" data-testid="confirm-banner"`) {
		t.Error("configure tab still warns about confirmation")
	}

	added := getBody(t, ts, h.app, "/repeaters/"+rep.PublicID+"/added", sess)
	if !strings.Contains(added, `data-testid="confirm-not-needed"`) {
		t.Error("post-add page doesn't say confirming isn't needed")
	}
	if strings.Contains(added, `class="btn btn-primary" href="/repeaters/`+rep.PublicID+`/configure"`) {
		t.Error("post-add page still makes connecting the primary action")
	}
	for _, stale := range []string{"Self-confirmed", "Corroborated"} {
		if strings.Contains(added, stale) {
			t.Errorf("post-add page still promises a %q badge that no longer exists", stale)
		}
	}
}

// TestPublicOrgPageShowsNetSchedule: the public org page shows the org's
// scheduled nets — what a visitor would be joining — to anonymous visitors and
// signed-in non-members alike, without linking into the members-only net pages
// and without inactive nets. The directory row names the next one.
func TestPublicOrgPageShowsNetSchedule(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, _ := appLogin(t, ts, st, ctx, h.app, "pubnetsadmin")
	_, visitor := appLogin(t, ts, st, ctx, h.app, "pubnetsvisitor")
	org, err := st.CreateOrg(ctx, "Public Nets Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	quiet, err := st.CreateOrg(ctx, "Quiet Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	weekly := netAround("Sunday Check-in", now.Add(2*time.Hour))
	if _, err := st.CreateNet(ctx, org.ID, weekly, admin.ID, now); err != nil {
		t.Fatal(err)
	}
	paused := netAround("Paused Net", now.Add(time.Hour))
	paused.Active = false
	if _, err := st.CreateNet(ctx, org.ID, paused, admin.ID, now); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{
		"anonymous (root host)": getBody(t, ts, h.root, "/orgs/"+org.Slug),
		"non-member (app host)": getBody(t, ts, h.app, "/orgs/"+org.Slug, visitor),
	} {
		_, nets, ok := strings.Cut(body, `data-testid="org-nets"`)
		if !ok || !strings.Contains(nets, "Sunday Check-in") || !strings.Contains(nets, weekly.Schedule()) {
			t.Errorf("%s: public page lacks the net schedule", name)
		}
		if strings.Contains(body, "Paused Net") {
			t.Errorf("%s: public page lists an inactive net", name)
		}
		if strings.Contains(body, `/orgs/`+org.Slug+`/nets/`) {
			t.Errorf("%s: public page links into the members-only net pages", name)
		}
	}
	if body := getBody(t, ts, h.root, "/orgs/"+quiet.Slug); strings.Contains(body, `data-testid="org-nets"`) {
		t.Error("an org without nets shows an empty Nets card publicly")
	}

	dir := getBody(t, ts, h.root, "/orgs?sort=name")
	_, row, _ := strings.Cut(dir, "Public Nets Org")
	row, _, _ = strings.Cut(row, `data-testid="org-row"`)
	if !strings.Contains(row, `data-testid="org-row-net"`) || !strings.Contains(row, "Sunday Check-in") || !strings.Contains(row, weekly.Schedule()) {
		t.Error("directory row doesn't name the org's next net")
	}
}
