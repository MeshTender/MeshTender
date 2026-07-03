package core

import (
	"strings"
	"testing"
)

// TestPublicOrgPageHidesMembersTab: the public org page never shows the Members
// tab — membership isn't public (only the admin list is), and the tab would 404
// on the root host anyway. It must stay hidden even for a signed-in member
// viewing the page via the identity beacon.
func TestPublicOrgPageHidesMembersTab(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	u, err := st.CreateUser(ctx, "orgmember", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "Tabs Org", u.ID) // creator is an admin member
	if err != nil {
		t.Fatal(err)
	}
	membersLink := "/orgs/" + org.Slug + "/members"

	anon := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug))
	if strings.Contains(anon, membersLink) {
		t.Fatal("anonymous public org page shows a Members tab")
	}

	// Drop a root identity cookie for the member (as a fresh sign-in would), then
	// view the public page as that member.
	loginID, _ := st.CreateLogin(ctx, u.ID)
	code, _ := st.CreateAuthCode(ctx, u.ID, loginID, "/")
	beacon := do(t, ts, h.root, "/session/beacon?code="+code)
	beacon.Body.Close()
	sess := cookieByName(beacon, "meshtender_session")
	if sess == nil {
		t.Fatal("beacon set no identity cookie")
	}

	member := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug, sess))
	if strings.Contains(member, membersLink) {
		t.Fatal("member viewing the public org page still sees the Members tab")
	}
	// The tabs that ARE public should still be there.
	if !strings.Contains(member, "/orgs/"+org.Slug+"/repeaters") {
		t.Fatal("public org page missing the Repeaters tab")
	}
}
