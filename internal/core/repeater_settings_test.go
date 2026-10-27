package core

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestRepeaterSettingsTab: the Settings tab is the owner's — every section
// renders for them with the rail, the tab shows on their pages only, and someone
// it's shared with gets a 404 and no tab. The pages it replaced redirect to their
// sections.
func TestRepeaterSettingsTab(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, ownerSess := appLogin(t, ts, st, ctx, h.app, "rset-owner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Hilltop")
	base := "/repeaters/" + rep.PublicID

	for path, marker := range map[string]string{
		base + "/settings":               `data-testid="settings-details-form"`,
		base + "/settings/docs":          `data-testid="settings-docs-form"`,
		base + "/settings/people":        `data-testid="new-invite"`,
		base + "/settings/organizations": `data-testid="repeater-settings-organizations"`,
		base + "/settings/ownership":     `data-testid="danger-zone"`,
	} {
		resp := do(t, ts, h.app, path, ownerSess)
		page := readBody(t, resp)
		if resp.StatusCode != http.StatusOK || !strings.Contains(page, marker) || !strings.Contains(page, `data-testid="repeater-settings-nav"`) {
			t.Errorf("GET %s = %d; want 200 with the rail and %s", path, resp.StatusCode, marker)
		}
	}
	if resp := do(t, ts, h.app, base+"/settings/nope", ownerSess); resp.StatusCode != http.StatusNotFound {
		resp.Body.Close()
		t.Errorf("unknown section = %d, want 404", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	for old, want := range map[string]string{
		base + "/edit":  base + "/settings",
		base + "/docs":  base + "/settings/docs",
		base + "/share": base + "/settings/people",
	} {
		resp := do(t, ts, h.app, old, ownerSess)
		resp.Body.Close()
		if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != want {
			t.Errorf("GET %s = %d → %q, want 301 → %s", old, resp.StatusCode, resp.Header.Get("Location"), want)
		}
	}

	if page := readBody(t, do(t, ts, h.app, base, ownerSess)); !strings.Contains(page, `data-testid="repeater-tab-settings"`) {
		t.Error("the owner's Overview has no Settings tab")
	}

	guest, guestSess := appLogin(t, ts, st, ctx, h.app, "rset-guest")
	if _, err := st.AddShare(ctx, rep.ID, guest.ID); err != nil {
		t.Fatal(err)
	}
	if page := readBody(t, do(t, ts, h.app, base, guestSess)); strings.Contains(page, `data-testid="repeater-tab-settings"`) {
		t.Error("someone it's shared with sees the Settings tab")
	}
	if resp := do(t, ts, h.app, base+"/settings/people", guestSess); resp.StatusCode != http.StatusNotFound {
		resp.Body.Close()
		t.Errorf("shared user GET settings = %d, want 404", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
}

// TestOverviewShowsDocumentation: the documentation moved from its own tab to
// the Overview, rendered as Markdown, internal notes included for people with
// access.
func TestOverviewShowsDocumentation(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, ownerSess := appLogin(t, ts, st, ctx, h.app, "rdoc-owner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Hilltop")
	if err := st.UpdateRepeaterDocs(ctx, owner.ID, rep.ID, "**Solar** on the roof", "Gate code 1234"); err != nil {
		t.Fatal(err)
	}
	base := "/repeaters/" + rep.PublicID

	page := readBody(t, do(t, ts, h.app, base, ownerSess))
	for _, want := range []string{"<strong>Solar</strong>", "Gate code 1234"} {
		if !strings.Contains(page, want) {
			t.Errorf("owner's Overview is missing %q", want)
		}
	}

	guest, guestSess := appLogin(t, ts, st, ctx, h.app, "rdoc-guest")
	if _, err := st.AddShare(ctx, rep.ID, guest.ID); err != nil {
		t.Fatal(err)
	}
	page = readBody(t, do(t, ts, h.app, base, guestSess))
	if !strings.Contains(page, "Gate code 1234") {
		t.Error("someone with access can't read the internal notes")
	}
}

// TestOverviewConfirmationWording is the regression for "You've reached it over
// the air" being shown to everyone: SelfConfirmed means the OWNER reached it, so
// only the owner reads "You've"; anyone else reads the owner's name.
func TestOverviewConfirmationWording(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, ownerSess := appLogin(t, ts, st, ctx, h.app, "rconf-owner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Hilltop")
	if err := st.SetRepeaterConfirmed(ctx, rep.ID, owner.ID, true, 3); err != nil {
		t.Fatal(err)
	}
	base := "/repeaters/" + rep.PublicID

	if page := readBody(t, do(t, ts, h.app, base, ownerSess)); !strings.Contains(page, "You've reached it over the air") {
		t.Error("the owner isn't told they've reached it")
	}

	guest, guestSess := appLogin(t, ts, st, ctx, h.app, "rconf-guest")
	if _, err := st.AddShare(ctx, rep.ID, guest.ID); err != nil {
		t.Fatal(err)
	}
	page := readBody(t, do(t, ts, h.app, base, guestSess))
	if strings.Contains(page, "You've reached it") {
		t.Error("someone who never connected is told they've reached it")
	}
	if !strings.Contains(page, "its owner, has reached it over the air") {
		t.Error("a shared viewer isn't told the owner reached it")
	}
}

// TestOrgAccessReturnsToWizard: the add-repeater wizard links to an
// organization's access page with ?next=added, and saving brings the owner back
// to the wizard. Only that fixed value does: anything else lands on Settings ›
// Organizations, so the parameter can't send anyone off-site.
func TestOrgAccessReturnsToWizard(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "rnext-owner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Hilltop")
	org, err := st.CreateOrg(ctx, "Next Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	action := "/repeaters/" + rep.PublicID + "/orgs/" + org.Slug + "/limits"

	page := readBody(t, do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/settings/organizations/"+org.Slug+"?next=added", sess))
	if !strings.Contains(page, `name="next" value="added"`) || !strings.Contains(page, `href="/repeaters/`+rep.PublicID+`/added"`) {
		t.Error("the page from the wizard should carry next=added and cancel back to it")
	}

	for next, want := range map[string]string{
		"added":               "/repeaters/" + rep.PublicID + "/added",
		"https://example.com": "/repeaters/" + rep.PublicID + "/settings/organizations",
		"":                    "/repeaters/" + rep.PublicID + "/settings/organizations",
	} {
		resp := post(t, ts, h.app, action, url.Values{"include": {"1"}, "next": {next}}, sess)
		resp.Body.Close()
		assertRedirect(t, resp, want, "save with next="+next)
	}
}
