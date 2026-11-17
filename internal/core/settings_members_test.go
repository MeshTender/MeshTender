package core

import (
	"strconv"
	"strings"
	"testing"
)

// TestMemberManagementInSettings: an admin changes roles and removes members on
// Settings › Members, like the rest of the org's editing — not on the Members
// tab, which is now the roster everyone sees. An admin can't act on themselves
// there, and a plain member can't reach the page at all (MESHTENDER-12).
func TestMemberManagementInSettings(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "membersadmin")
	member, memberSess := appLogin(t, ts, st, ctx, h.app, "membersplain")
	org, err := st.CreateOrg(ctx, "Members Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	memberAction := `action="/orgs/` + org.Slug + `/members/` + strconv.FormatInt(member.ID, 10) + `"`
	selfAction := `action="/orgs/` + org.Slug + `/members/` + strconv.FormatInt(admin.ID, 10) + `"`

	settings := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/settings/members", sess)
	for _, want := range []string{memberAction, `value="promote"`, `value="remove"`, `data-testid="org-settings-members"`, "membersplain"} {
		if !strings.Contains(settings, want) {
			t.Errorf("Settings › Members lacks %q", want)
		}
	}
	if strings.Contains(settings, selfAction) {
		t.Error("Settings › Members lets an admin act on themselves")
	}

	tab := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/members", sess)
	if strings.Contains(tab, memberAction) || strings.Contains(tab, `value="promote"`) {
		t.Error("the Members tab still has member actions")
	}
	if !strings.Contains(tab, "membersplain") {
		t.Error("the admin's Members tab lacks the roster")
	}
	if got := status(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/settings/members", memberSess)); got != 404 {
		t.Errorf("a plain member GET Settings › Members = %d, want 404", got)
	}
}
