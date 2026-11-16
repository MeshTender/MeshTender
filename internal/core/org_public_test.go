package core

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestOrgSummarySetting: Settings › Profile saves the summary as one line, and
// refuses one over store.MaxOrgSummary characters — re-showing what was typed —
// rather than cutting it off mid-word.
func TestOrgSummarySetting(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "summaryadmin")
	org, err := st.CreateOrg(ctx, "Summary Settings Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	form := func(summary string) url.Values {
		return url.Values{"name": {org.Name}, "slug": {org.Slug}, "summary": {summary}, "description": {"About us."}}
	}

	resp := post(t, ts, h.app, "/orgs/"+org.Slug+"/edit", form("  Weekly check-in net;\n new members   welcome. "), sess)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save = %d, want 303", resp.StatusCode)
	}
	if got, _ := st.GetOrg(ctx, org.ID); got.Summary != "Weekly check-in net; new members welcome." {
		t.Fatalf("saved summary = %q, want it on one line", got.Summary)
	}

	long := strings.Repeat("x", store.MaxOrgSummary+1)
	resp = post(t, ts, h.app, "/orgs/"+org.Slug+"/edit", form(long), sess)
	body := readBody(t, resp)
	if !strings.Contains(body, "Keep the summary to 160 characters") || !strings.Contains(body, `value="`+long+`"`) {
		t.Error("an over-long summary isn't refused with what was typed re-shown")
	}
	if got, _ := st.GetOrg(ctx, org.ID); got.Summary != "Weekly check-in net; new members welcome." {
		t.Errorf("an over-long summary replaced the saved one: %q", got.Summary)
	}
	if page := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/settings", sess); !strings.Contains(page, `maxlength="160"`) ||
		!strings.Contains(page, `data-char-count="#org_summary_count"`) {
		t.Error("the summary field lacks its limit or character count")
	}
}

// TestPublicOrgPagePitch: the public org page makes the case for joining this
// org — the summary up top with its size, when it next meets (with the Guide one
// link away for how to take part), the full markdown About, who runs it, and the
// call to action again at the bottom. An org without a summary, description or
// nets shows none of them rather than placeholders.
func TestPublicOrgPagePitch(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, _ := appLogin(t, ts, st, ctx, h.app, "pitchadmin")
	_, visitor := appLogin(t, ts, st, ctx, h.app, "pitchvisitor")
	org, err := st.CreateOrg(ctx, "Pitch Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrg(ctx, org.ID, store.OrgProfile{Slug: org.Slug, Name: org.Name, Region: "Lakeshore",
		Summary: "Weekly check-in net; new members welcome.", Description: "We run **solar** repeaters."}); err != nil {
		t.Fatal(err)
	}
	radio := defaultRadio()
	if err := st.SetOrgRadio(ctx, org.ID, &radio); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := st.CreateNet(ctx, org.ID, netAround("Sunday Check-in", now.Add(2*time.Hour)), admin.ID, now); err != nil {
		t.Fatal(err)
	}

	anon := getBody(t, ts, h.root, "/orgs/"+org.Slug)
	assertInOrder(t, "public page", anon,
		`data-testid="public-summary">Weekly check-in net; new members welcome.<`,
		`data-testid="public-counts"`, "weekly net<",
		`data-testid="public-next-net"`, "Sunday Check-in", `href="/orgs/`+org.Slug+`/guide" data-testid="public-guide-link"`,
		`data-testid="public-about"`, "About Pitch Org", "<strong>solar</strong>",
		`data-testid="public-who"`, "pitchadmin",
		`data-testid="public-join"`)
	// The header carries the same call to action, so look at the bottom's own.
	if _, bottom, _ := strings.Cut(anon, `data-testid="public-join"`); !strings.Contains(bottom, "Sign in to join") {
		t.Error("an anonymous visitor isn't asked to sign in to join at the bottom")
	}
	body := getBody(t, ts, h.app, "/orgs/"+org.Slug, visitor)
	if _, bottom, _ := strings.Cut(body, `data-testid="public-join"`); !strings.Contains(bottom, "Join organization") {
		t.Error("a signed-in non-member isn't asked to join at the bottom")
	}

	plain, err := st.CreateOrg(ctx, "Plain Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	body = getBody(t, ts, h.root, "/orgs/"+plain.Slug)
	for _, absent := range []string{`data-testid="public-summary"`, `data-testid="public-about"`, `data-testid="public-next-net"`, "hasn"} {
		if strings.Contains(body, absent) {
			t.Errorf("an org without a summary, description or nets shows %s", absent)
		}
	}
	if !strings.Contains(body, `data-testid="public-guide-link"`) {
		t.Error("an org without nets doesn't link its Guide")
	}
}

// TestOrgBlurbs: in the directory and My orgs an org is its summary; until it has
// one, its description stands in, clamped so a long About can't swamp the list.
func TestOrgBlurbs(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "blurbadmin")
	summarized, err := st.CreateOrg(ctx, "Summarized Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrg(ctx, summarized.ID, store.OrgProfile{Slug: summarized.Slug, Name: summarized.Name,
		Summary: "The short version.", Description: "The long version."}); err != nil {
		t.Fatal(err)
	}
	described, err := st.CreateOrg(ctx, "Described Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrg(ctx, described.ID, store.OrgProfile{Slug: described.Slug, Name: described.Name,
		Description: "Only a **long** description."}); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{
		"directory": getBody(t, ts, h.root, "/orgs?sort=name"),
		"My orgs":   getBody(t, ts, h.app, "/orgs", sess),
	} {
		if !strings.Contains(body, `data-testid="org-blurb-summary">The short version.<`) || strings.Contains(body, "The long version.") {
			t.Errorf("%s: an org with a summary isn't shown by it", name)
		}
		if !strings.Contains(body, `text-clamp-2" data-testid="org-blurb-description">Only a long description.<`) {
			t.Errorf("%s: an org without a summary doesn't fall back to its clamped description", name)
		}
	}
}
