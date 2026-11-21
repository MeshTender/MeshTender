package core

import (
	"strings"
	"testing"
)

// landingHero returns the landing page's hero section.
func landingHero(t *testing.T, page string) string {
	t.Helper()
	start := strings.Index(page, `data-testid="landing-hero"`)
	if start < 0 {
		t.Fatalf("landing page has no hero section")
	}
	end := strings.Index(page[start:], "</section>")
	if end < 0 {
		t.Fatalf("landing hero section is never closed")
	}
	return page[start : start+end]
}

// TestLandingLeadsWithMembers: the landing page speaks to everyday MeshCore
// users first and keeps the KISS-modem pitch for organization admins, further
// down (MESHTENDER-57). The over-the-air console is still the main selling
// point, but only admins and some owners have a modem, so it isn't the first
// thing everyone reads. What a member needs is the companion radio, not an app.
func TestLandingLeadsWithMembers(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	t.Run("signed out", func(t *testing.T) {
		page := readBody(t, do(t, ts, h.root, "/"))
		assertInOrder(t, "landing sections", page,
			"For members", "For repeater owners", "For organization admins", "Manage your repeaters over the air")

		hero := landingHero(t, page)
		if strings.Contains(hero, "KISS") {
			t.Errorf("the hero mentions the KISS modem; it should speak to members")
		}
		if !strings.Contains(hero, `/orgs"`) || !strings.Contains(hero, `/signup"`) {
			t.Errorf("signed-out hero should offer the organization directory and sign-up")
		}
		for _, phrase := range []string{"companion app", "already use"} {
			if strings.Contains(page, phrase) {
				t.Errorf("landing page says %q; name the companion radio instead", phrase)
			}
		}
	})

	t.Run("signed in", func(t *testing.T) {
		u, err := st.CreateUser(ctx, "landingmember", "")
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		page := readBody(t, do(t, ts, h.root, "/", rootSession(t, ts, st, ctx, h.root, u)))
		hero := landingHero(t, page)
		if !strings.Contains(hero, "Go to your dashboard") {
			t.Errorf("signed-in hero should link to the dashboard")
		}
		if strings.Contains(page, `/signup"`) {
			t.Errorf("signed-in landing page still offers sign-up")
		}
	})
}
