package core

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var orgCountRe = regexp.MustCompile(`data-testid="org-count"[^>]*>\s*([0-9]+)\s+organizations?`)

// TestOrgDirectoryStatesItsSize pins audit U5: the directory showed 50 rows with no
// indication whether that was the whole thing or a slice.
//
// The number is a filter-wide total, not a tally of rows on screen — the "Show more"
// control appends pages via htmx without replacing the page header, so a running count
// there would be wrong the moment anyone used it. The trade is that the total can exceed
// the rows visible, which is exactly the information that was missing.
func TestOrgDirectoryStatesItsSize(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	get := func(path string) string {
		t.Helper()
		resp := do(t, ts, h.root, path)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d, want 200", path, resp.StatusCode)
		}
		return string(body)
	}
	advertised := func(html, where string) int {
		t.Helper()
		m := orgCountRe.FindStringSubmatch(html)
		if m == nil {
			t.Fatalf("%s: no organization count rendered", where)
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: unparseable count %q", where, m[1])
		}
		return n
	}

	// Empty directory: the count must still render, and say zero rather than nothing.
	if got := advertised(get("/orgs"), "empty directory"); got != 0 {
		t.Errorf("empty directory advertises %d organizations, want 0", got)
	}

	// Seed a known number, one of which is findable by a distinctive search term.
	owner, err := st.CreateUser(ctx, "countowner", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	const seeded = 7
	for i := 0; i < seeded; i++ {
		name := fmt.Sprintf("Count Org %d", i)
		if i == 0 {
			name = "Zarquon Ridge Mesh"
		}
		if _, err := st.CreateOrg(ctx, name, owner.ID); err != nil {
			t.Fatalf("create org %d: %v", i, err)
		}
	}

	html := get("/orgs")
	if got := advertised(html, "seeded directory"); got != seeded {
		t.Errorf("directory advertises %d organizations, want %d", got, seeded)
	}
	// The count has to agree with what's actually listed, or it contradicts the rows
	// right beneath it. Both come from the same search predicate for this reason.
	if rows := strings.Count(html, `data-testid="org-row"`); rows > 0 && rows != seeded {
		t.Errorf("advertised %d organizations but rendered %d rows", seeded, rows)
	}

	// With a search active the count must describe the matches, not the directory.
	filtered := get("/orgs?q=Zarquon")
	if got := advertised(filtered, "filtered directory"); got != 1 {
		t.Errorf("search for Zarquon advertises %d organizations, want 1", got)
	}
	if !strings.Contains(filtered, "matching") {
		t.Error("filtered count doesn't say it's describing matches")
	}
	// Singular vs plural, since the count is user-facing copy.
	if strings.Contains(filtered, "1 organizations") {
		t.Error(`filtered count reads "1 organizations"`)
	}

	// The "load more" fragment must NOT carry a count. It's swapped into the list, not
	// the header, so a count inside it would either be discarded or — worse, if someone
	// later wires it up — overwrite the total with a per-page number.
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/orgs", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = h.root
	req.Header.Set("HX-Request", "true")
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatalf("fragment request: %v", err)
	}
	frag, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read fragment: %v", err)
	}
	if strings.Contains(string(frag), `data-testid="org-count"`) {
		t.Error("the htmx fragment carries an organization count; it should only render rows")
	}
	if strings.Contains(string(frag), "<no value>") {
		t.Error("the fragment references a data key the fragment path doesn't supply")
	}
	if !strings.Contains(string(frag), `data-testid="org-row"`) {
		t.Error("the fragment rendered no rows — fixture or layout problem")
	}
}
