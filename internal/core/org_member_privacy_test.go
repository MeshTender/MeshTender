package core

import (
	"net/http"
	"strings"
	"testing"
)

// The privacy page states two things about organization membership: the member
// list is visible only to members, and admins are named publicly. Both are
// promises about who can see a person's name, so they're worth binding to the
// behavior rather than trusting the copy to stay true — the wording was wrong
// once already, claiming the member list was visible to anyone who could see the
// organization.
func TestOrgMemberListIsMembersOnlyButAdminsArePublic(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	admin, adminSess := appLogin(t, ts, st, ctx, h.app, "orgadminpub")
	org, err := st.CreateOrg(ctx, "Privacy Org", admin.ID) // creator is an admin member
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateUser(ctx, "quietmember", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	_, outsiderSess := appLogin(t, ts, st, ctx, h.app, "nosyoutsider")

	membersPath := "/orgs/" + org.Slug + "/members"

	// A signed-in non-member gets a 404, not a redirect or an empty page: the list
	// carries usernames, and even confirming the page exists says who's asking about.
	resp := do(t, ts, h.app, membersPath, outsiderSess)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("non-member GET %s = %d, want 404", membersPath, resp.StatusCode)
	}

	// A member sees the roster, which is what makes the page worth protecting.
	memberView := readBody(t, do(t, ts, h.app, membersPath, adminSess))
	if !strings.Contains(memberView, "quietmember") {
		t.Error("member's view of the roster doesn't list the other member")
	}

	// The public page names admins and links their profiles, and says how many
	// members there are without naming them.
	publicView := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug))
	if !strings.Contains(publicView, "/u/orgadminpub") {
		t.Error("public org page doesn't name and link the org's admin")
	}
	if strings.Contains(publicView, "quietmember") {
		t.Error("public org page names a plain member; membership is not public")
	}
}
