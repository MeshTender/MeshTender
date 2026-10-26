package core

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestDashboard: a net in session in any of the user's organizations gets a
// banner linking to the session; each organization shows its next net; the
// radios card counts companions and repeaters with what needs attention; and
// the checklist asks for a companion, not a repeater.
func TestDashboard(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	user, sess := appLogin(t, ts, st, ctx, h.app, "dashuser")

	empty := getBody(t, ts, h.app, "/", sess)
	for _, want := range []string{"Register your companion", "Join an organization", `data-testid="dashboard-radios"`, "not in an organization yet"} {
		if !strings.Contains(empty, want) {
			t.Errorf("new user's dashboard lacks %q", want)
		}
	}
	if strings.Contains(empty, "Add a repeater") {
		t.Error("the checklist still asks for a repeater")
	}

	org, err := st.CreateOrg(ctx, "Dash Org", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	live, err := st.CreateNet(ctx, org.ID, netAround("Dash Net", now.Add(-5*time.Minute)), user.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.NextNetSession(ctx, live.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCompanion(ctx, user.ID, "Dash T1", strings.Repeat("ef", 32)); err != nil {
		t.Fatal(err)
	}

	body := getBody(t, ts, h.app, "/", sess)
	sessionPath := "/orgs/" + org.Slug + "/nets/" + strconv.FormatInt(live.ID, 10) + "/sessions/" + strconv.FormatInt(session.ID, 10)
	if _, banner, ok := strings.Cut(body, `data-testid="dashboard-live-net"`); !ok || !strings.Contains(banner, `href="`+sessionPath+`"`) {
		t.Error("no banner linking to the session in progress")
	}
	if _, orgs, _ := strings.Cut(body, `data-testid="dashboard-orgs"`); !strings.Contains(orgs, "Dash Org") || !strings.Contains(orgs, "in session now") {
		t.Error("Your organizations lacks the org and its net in session")
	}
	if _, radios, _ := strings.Cut(body, `data-testid="dashboard-radios"`); !strings.Contains(radios, "1 not heard yet") {
		t.Error("Your radios doesn't flag the companion not heard yet")
	}
}
