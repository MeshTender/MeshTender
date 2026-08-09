package core

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestRootOrgDirectoryLoadMore verifies the directory's htmx "load more": the
// full page carries chrome + a load-more control, and an HX-Request returns just
// the next rows as a chrome-less fragment to append in place.
func TestRootOrgDirectoryLoadMore(t *testing.T) {
	st, ctx, ts, h := splitServer(t)

	u, err := st.CreateUser(ctx, "creator", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	for i := 0; i < store.OrgsPageSize+1; i++ { // 51 orgs => one full page + a "show more"
		if _, err := st.CreateOrg(ctx, fmt.Sprintf("Org %02d", i), u.ID); err != nil {
			t.Fatalf("create org: %v", err)
		}
	}

	full := readBody(t, do(t, ts, h.root, "/orgs"))
	if !strings.Contains(full, "<!doctype html") {
		t.Fatalf("full page missing chrome")
	}
	if !strings.Contains(full, `id="org-list"`) {
		t.Fatalf("full page missing list container")
	}
	if !strings.Contains(full, "Show more organizations") {
		t.Fatalf("full page missing load-more control")
	}

	cursor := between(full, "/orgs?cursor=", `"`)
	if cursor == "" {
		t.Fatalf("no cursor found in load-more control")
	}

	// htmx request → chrome-less fragment of just the next rows.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/orgs?cursor="+cursor, nil)
	req.Host = h.root
	req.Header.Set("HX-Request", "true")
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatalf("htmx request: %v", err)
	}
	frag := readBody(t, resp)
	if strings.Contains(frag, "<!doctype html") || strings.Contains(frag, "navbar") {
		t.Fatalf("htmx fragment should not include page chrome")
	}
	if !strings.Contains(frag, "list-group-item") {
		t.Fatalf("htmx fragment missing org rows")
	}
}

// TestOrgPublicRepeatersLoadMore verifies #10: the public Repeaters tab paginates
// its list with htmx "show more" while the map still shows all located repeaters —
// now fetched from the cached JSON endpoint rather than inlined into the HTML.
func TestOrgPublicRepeatersLoadMore(t *testing.T) {
	st, ctx, ts, h := splitServer(t)

	u, err := st.CreateUser(ctx, "ownerp", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	org, err := st.CreateOrg(ctx, "Pager Club", u.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	for i := 0; i < store.OrgPublicRepeatersPageSize+1; i++ { // 26 => one full page + more
		rep, err := st.CreateRepeater(ctx, &store.Repeater{
			OwnerID: u.ID, Name: fmt.Sprintf("Node %02d", i), PublicKeyHex: fmt.Sprintf("%064x", i+1),
			RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5, ShowOnPublicOrg: true,
		})
		if err != nil {
			t.Fatalf("create repeater: %v", err)
		}
		if i%2 == 0 {
			_ = st.SetRepeaterLocation(ctx, rep.ID, 40.0+float64(i)/100, -75.0)
		}
	}

	full := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug+"/repeaters"))
	if !strings.Contains(full, `id="rep-list"`) {
		t.Fatalf("full page missing list container")
	}
	if !strings.Contains(full, "Show more repeaters") {
		t.Fatalf("full page missing load-more control")
	}
	// The public map now loads its points from the cached JSON endpoint instead of
	// inlining them, so the page wires up meshMapFromURL against that endpoint.
	if !strings.Contains(full, `meshMapFromURL('map', "/orgs/`+org.Slug+`/repeaters.json")`) {
		t.Fatalf("full page missing the fetched map")
	}

	cursor := between(full, "/repeaters?cursor=", `"`)
	if cursor == "" {
		t.Fatalf("no cursor in load-more control")
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/orgs/"+org.Slug+"/repeaters?cursor="+cursor, nil)
	req.Host = h.root
	req.Header.Set("HX-Request", "true")
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatalf("htmx request: %v", err)
	}
	frag := readBody(t, resp)
	if strings.Contains(frag, "<!doctype html") || strings.Contains(frag, "navbar") {
		t.Fatalf("htmx fragment should not include page chrome")
	}
	if !strings.Contains(frag, "list-group-item") {
		t.Fatalf("htmx fragment missing repeater rows")
	}
}

func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	s = s[i+len(start):]
	j := strings.Index(s, end)
	if j < 0 {
		return ""
	}
	return s[:j]
}
