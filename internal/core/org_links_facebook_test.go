package core

import (
	"net/url"
	"strings"
	"testing"
)

// TestOrgLinkKeepsFacebookShareLink: a Facebook group share link saved in org
// Settings keeps its URL and shows as "Facebook" on the public page — not as
// the handle "@1HjZ7Bg2WX" linking to facebook.com/1HjZ7Bg2WX, a different page
// (MESHTENDER-14).
func TestOrgLinkKeepsFacebookShareLink(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "fbadmin")
	org, err := st.CreateOrg(ctx, "FB Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	const share = "https://www.facebook.com/share/g/1HjZ7Bg2WX/"
	post(t, ts, h.app, "/orgs/"+org.Slug+"/links",
		url.Values{"link_platform": {"facebook"}, "link_label": {""}, "link_url": {share}}, sess).Body.Close()

	links, err := st.ListOrgLinks(ctx, org.ID)
	if err != nil || len(links) != 1 || links[0].URL != share {
		t.Fatalf("stored links = %+v, %v; want the share link as pasted", links, err)
	}
	body := getBody(t, ts, h.root, "/orgs/"+org.Slug)
	if !strings.Contains(body, `href="`+share+`"`) || strings.Contains(body, "@1HjZ7Bg2WX") {
		t.Error("the public page doesn't link the share link as pasted, or shows it as a handle")
	}
	if _, links, _ := strings.Cut(body, `data-testid="org-links"`); !strings.Contains(links, ">Facebook</span>") {
		t.Error("the share link isn't shown as Facebook")
	}
}
