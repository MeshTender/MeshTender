package core

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/store"
)

var headingRe = regexp.MustCompile(`(?i)<h([1-6])\b`)

// checkHeadingOutline asserts the two properties that make a page navigable by
// heading: it has exactly one <h1> naming the page, and no level is skipped on the
// way down (an h1 followed by an h3 leaves a hole in the outline that screen-reader
// users navigating by level fall through).
func checkHeadingOutline(t *testing.T, label, html string) {
	t.Helper()

	var levels []int
	for _, m := range headingRe.FindAllStringSubmatch(html, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: unparseable heading %q", label, m[0])
		}
		levels = append(levels, n)
	}
	if len(levels) == 0 {
		t.Errorf("%s: page has no headings at all", label)
		return
	}

	h1s := 0
	for _, l := range levels {
		if l == 1 {
			h1s++
		}
	}
	switch {
	case h1s == 0:
		t.Errorf("%s: no <h1> — the page has no top-level heading (outline starts at h%d)",
			label, levels[0])
	case h1s > 1:
		t.Errorf("%s: %d <h1> elements — exactly one should name the page; sequence: %s",
			label, h1s, seq(levels))
	}
	if levels[0] != 1 {
		t.Errorf("%s: outline starts at h%d, not h1 (sequence: %s)", label, levels[0], seq(levels))
	}
	for i := 1; i < len(levels); i++ {
		if levels[i] > levels[i-1]+1 {
			t.Errorf("%s: heading level jumps h%d → h%d (skips h%d); sequence: %s",
				label, levels[i-1], levels[i], levels[i-1]+1, seq(levels))
			break // one report per page is enough to act on
		}
	}
}

func seq(levels []int) string {
	parts := make([]string, len(levels))
	for i, l := range levels {
		parts[i] = fmt.Sprintf("h%d", l)
	}
	return strings.Join(parts, " ")
}

// TestPageHeadingOutlines walks a representative page from every layout and header
// source — the default base header, the repeater-tabs header, the org-tabs header,
// the auth layout (which has no page header, so the card title carries it), the
// public root layout, and the branded error page — and checks each one's heading
// outline.
//
// Regression for audit findings A1 (28 pages had no <h1>; page titles were
// <h2 class="page-title">) and A2 (the landing page ran h1 → h3 → h2, skipping a
// level and then jumping backwards).
func TestPageHeadingOutlines(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	user := seedSession(t, ts, st, ctx, jar, "headinguser")
	cookies := jar.Cookies(mustURL(t, ts.URL))

	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Heading Test Relay", PublicKeyHex: strings.Repeat("e", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	org, err := st.CreateOrg(ctx, "Heading Test Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	pages := []struct {
		label, host, path string
		anon              bool // fetch without the session cookie
	}{
		// Root (public) host — the landing page is A2's subject.
		{"root landing", h.root, "/", false},
		{"root org directory", h.root, "/orgs", false},
		{"root docs", h.root, "/docs", false},
		{"root public org page", h.root, "/orgs/" + org.Slug, false},
		{"root 404 (error page)", h.root, "/no-such-page", false},
		// Auth host — authbase has no page header, so the card title is the heading.
		{"auth login", h.auth, "/login", true},
		{"auth signup", h.auth, "/signup", true},
		// App host — covers base's own header, repeater-tabs and org-tabs headers.
		{"app dashboard", h.app, "/", false},
		{"app repeater list", h.app, "/repeaters", false},
		{"app repeater overview", h.app, "/repeaters/" + rep.PublicID, false},
		{"app repeater sharing", h.app, "/repeaters/" + rep.PublicID + "/share", false},
		{"app my orgs", h.app, "/orgs", false},
		{"app org member view", h.app, "/orgs/" + org.Slug, false},
		{"app 404 (error page)", h.app, "/no-such-page", false},
	}

	for _, p := range pages {
		send := cookies
		if p.anon {
			send = nil
		}
		resp := do(t, ts, p.host, p.path, send...)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("%s: read body: %v", p.label, err)
		}
		// A redirect would mean the fixture didn't grant access; the outline of a
		// redirect body is meaningless, so surface it rather than silently passing.
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			t.Fatalf("%s: unexpected %d redirect to %q — fixture problem",
				p.label, resp.StatusCode, resp.Header.Get("Location"))
		}
		checkHeadingOutline(t, p.label, string(body))
	}
}

// TestModalFragmentHeadings covers the htmx modal fragments separately: they're
// swapped into an already-rendered page, so their heading has to fit under that
// page's outline (h1 page → h2 dialog title) rather than restart it.
func TestModalFragmentHeadings(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	user := seedSession(t, ts, st, ctx, jar, "modaluser")
	cookies := jar.Cookies(mustURL(t, ts.URL))
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Modal Test Relay", PublicKeyHex: strings.Repeat("f", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}

	// The invite modal is the representative fragment; all modal titles share the
	// same convention.
	resp := do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/share/link/new", cookies...)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invite modal fragment = %d, want 200", resp.StatusCode)
	}

	found := headingRe.FindAllStringSubmatch(string(body), -1)
	if len(found) == 0 {
		t.Fatal("invite modal fragment has no heading — the dialog has nothing to label it")
	}
	if got := found[0][1]; got != "2" {
		t.Errorf("modal title is h%s, want h2 (it sits under the page's h1; an h5 there "+
			"would skip three levels)", got)
	}
}
