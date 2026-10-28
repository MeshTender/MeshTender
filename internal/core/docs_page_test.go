package core

import (
	"strings"
	"testing"
)

// TestDocsPage renders the public help page on the root host and confirms it
// shows the live setperm command (with the instance's real key filled in) and
// the key sidebar — the whole reason the page exists.
func TestDocsPage(t *testing.T) {
	_, _, ts, h := splitServer(t)
	body := readBody(t, do(t, ts, h.root, "/docs"))
	for _, want := range []string{
		"How MeshTender works",
		"Granting MeshTender admin",
		"setperm ", // the live command, e.g. "setperm <hex> 3"
		"MeshTender server public key",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("docs page missing %q", want)
		}
	}
	// The literal placeholder must NOT appear — the key should be filled in.
	if strings.Contains(body, "setperm &lt;MeshTender") || strings.Contains(body, "<key>") {
		t.Fatalf("docs page shows a placeholder instead of the real setperm command")
	}
}

// TestDocsRegionsPage renders the regions explainer on the root host and checks
// the docs page links to it — the link is how people find it.
func TestDocsRegionsPage(t *testing.T) {
	_, _, ts, h := splitServer(t)
	body := readBody(t, do(t, ts, h.root, "/docs/regions"))
	for _, want := range []string{
		"Regions &amp; scoping",
		"Why companions should wait",
		"region def ca on to",
		"Keep repeating unscoped messages", // denyf * strands new, unscoped companions
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("regions page missing %q", want)
		}
	}
	docs := readBody(t, do(t, ts, h.root, "/docs"))
	if !strings.Contains(docs, `href="/docs/regions"`) {
		t.Fatalf("docs page doesn't link to /docs/regions")
	}
}
