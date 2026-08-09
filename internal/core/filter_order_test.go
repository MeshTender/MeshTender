package core

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestFilterControlsPrecedeTheList pins audit U2 across every list page that has a
// search/filter sidebar.
//
// The sidebar renders to the right of the list at ≥lg, but it must come FIRST in the
// DOM, with `order-lg-last` doing the visual placement. Source order is what governs
// both the mobile stack (columns stack in DOM order, so a trailing sidebar lands below
// ~50 rows) and the tab / screen-reader order. Fixing only the visuals with CSS would
// leave keyboard users traversing the whole list to reach the control that exists to
// avoid exactly that — trading a visible problem for an invisible one, which is why
// this asserts on order rather than on a CSS class alone.
func TestFilterControlsPrecedeTheList(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	user := seedSession(t, ts, st, ctx, jar, "filterorder")
	// The admin user list needs the manage-users capability to render at all.
	if err := st.SetCapabilities(ctx, user.ID, true, true); err != nil {
		t.Fatalf("grant capabilities: %v", err)
	}
	// Each of these pages renders its filter sidebar only when it has content, so the
	// fixtures below are what keep the assertions from being vacuous.
	if _, err := st.CreateOrg(ctx, "Filter Order Org", user.ID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Filter Order Relay", PublicKeyHex: strings.Repeat("a", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	}); err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	cookies := jar.Cookies(mustURL(t, ts.URL))

	for _, page := range []struct {
		label, host, path string
		// searchMarker identifies the filter control; listMarker the list container.
		searchMarker, listMarker string
	}{
		{"public org directory", h.root, "/orgs", `id="org-q"`, `id="org-list"`},
		{"your repeaters", h.app, "/repeaters", `data-filter-search`, `data-filter-item`},
		{"admin user list", h.app, "/admin/users", `id="user-q"`, `data-testid="user-row"`},
	} {
		resp := do(t, ts, page.host, page.path, cookies...)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("%s: read body: %v", page.label, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %s = %d, want 200", page.label, page.path, resp.StatusCode)
		}
		html := string(body)

		searchAt := strings.Index(html, page.searchMarker)
		if searchAt < 0 {
			t.Errorf("%s: no search control (%s) found", page.label, page.searchMarker)
			continue
		}
		// A page with no rows can't demonstrate the ordering; skip rather than pass
		// vacuously, so an empty fixture doesn't silently hollow this out.
		listAt := strings.Index(html, page.listMarker)
		if listAt < 0 {
			t.Errorf("%s: no list container (%s) found — fixture problem, the ordering "+
				"assertion below would be meaningless", page.label, page.listMarker)
			continue
		}
		if searchAt > listAt {
			t.Errorf("%s: the search control comes AFTER the list in the DOM, so it stacks "+
				"below every row on mobile and lands last in the tab order", page.label)
		}
		// And the visual desktop placement must be preserved by the ordering utility,
		// not by source order.
		if !strings.Contains(html, "order-lg-last") {
			t.Errorf("%s: missing order-lg-last, so the sidebar will render above the list "+
				"on desktop instead of beside it", page.label)
		}
	}
}
