package core

import (
	"crypto/rand"
	"strings"
	"testing"

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
		`data-testid="org-tab-home"`, `data-testid="org-tab-checkins"`, `data-testid="org-tab-members"`,
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
