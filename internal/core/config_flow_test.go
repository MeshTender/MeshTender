package core

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestOrgConfigProfilesFlow exercises #11 end to end: an admin saves multiple
// named profiles + a region, the read view shows them with a profile selector,
// and the Configuration tab hides on a config-less org's public page. Steps are
// comment-only so the flow doesn't depend on the seeded command catalog.
func TestOrgConfigProfilesFlow(t *testing.T) {
	st, ctx, ts, h := splitServer(t)

	// Sign in via the handoff (the splitServer-proven pattern) and grab the app
	// session cookie to carry on later requests.
	admin, _ := st.CreateUser(ctx, "cfgadmin", "")
	loginID, _ := st.CreateLogin(ctx, admin.ID)
	code, _ := st.CreateAuthCode(ctx, admin.ID, loginID, "/")
	cb := do(t, ts, h.app, "/session/callback?code="+code+"&state=s1", &http.Cookie{Name: "mt_state", Value: "s1"})
	cb.Body.Close()
	sess := cookieByName(cb, "meshtender_session")
	if sess == nil {
		t.Fatal("no app session from callback")
	}

	org, err := st.CreateOrg(ctx, "Config Org", admin.ID) // creator becomes admin
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	form := url.Values{
		"profile_name":    {"ESP32", "nRF52"},
		"profile_steps":   {"# esp base", "# nrf base"},
		"region_name":     {"Mountains"},
		"region_priority": {"5"},
		"region_minlat":   {"10"}, "region_minlon": {"30"},
		"region_maxlat": {"20"}, "region_maxlon": {"40"},
		"region_steps": {"# mountain note"},
	}
	save := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/edit", form, sess)
	save.Body.Close()
	if save.StatusCode != http.StatusSeeOther {
		t.Fatalf("save status = %d, want 303", save.StatusCode)
	}

	body := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config", sess))
	for _, want := range []string{"ESP32", "nRF52", "Mountains", "<select"} {
		if !strings.Contains(body, want) {
			t.Fatalf("config view missing %q", want)
		}
	}
	// Default selection shows the first profile's base settings.
	if !strings.Contains(body, "ESP32 · base settings") {
		t.Fatalf("default config view should show the first profile's base settings")
	}
	// The profile selector works: ?profile= switches the shown base settings.
	sel := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config?profile=nRF52", sess))
	if !strings.Contains(sel, "nRF52 · base settings") {
		t.Fatalf("?profile=nRF52 should show nRF52's base settings")
	}

	// A non-admin member of a config-less org does NOT see the Configuration tab.
	plain, _ := st.CreateUser(ctx, "plainmember", "")
	noCfg, _ := st.CreateOrg(ctx, "NoCfg Org", admin.ID)
	if err := st.AddOrgMember(ctx, noCfg.ID, plain.ID, "member"); err != nil {
		t.Fatalf("add member: %v", err)
	}
	pLogin, _ := st.CreateLogin(ctx, plain.ID)
	pCode, _ := st.CreateAuthCode(ctx, plain.ID, pLogin, "/")
	pcb := do(t, ts, h.app, "/session/callback?code="+pCode+"&state=s1", &http.Cookie{Name: "mt_state", Value: "s1"})
	pcb.Body.Close()
	pSess := cookieByName(pcb, "meshtender_session")
	memberView := readBody(t, do(t, ts, h.app, "/orgs/"+noCfg.Slug, pSess))
	if strings.Contains(memberView, "/orgs/"+noCfg.Slug+"/config") {
		t.Fatalf("a non-admin member should not see the config tab when there's no config")
	}

	// A config-less org hides the Configuration tab on its public page; the
	// configured org shows it.
	empty, _ := st.CreateOrg(ctx, "Empty Org", admin.ID)
	pub := readBody(t, do(t, ts, h.root, "/orgs/"+empty.Slug))
	if strings.Contains(pub, "/orgs/"+empty.Slug+"/config") {
		t.Fatalf("public org page should hide the config tab when there's no config")
	}
	pub2 := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug))
	if !strings.Contains(pub2, "/orgs/"+org.Slug+"/config") {
		t.Fatalf("public org page should show the config tab when config exists")
	}
}
