package licenses

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/licensecheck"
)

// minCoverage is how much of a license text licensecheck must recognize before
// we trust the declared SPDX ID. Real license files score 97-100%; a truncated
// or hand-mangled one scores far lower, which is exactly what we want to catch.
const minCoverage = 90.0

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod above the test directory")
		}
		dir = parent
	}
}

// TestManifestEntriesAreWellFormed checks the shape of each entry, so the later
// tests can assume the fields they need are populated.
func TestManifestEntriesAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Deps {
		if d.Name == "" {
			t.Fatal("a manifest entry has no Name")
		}
		if seen[d.Label()] {
			t.Errorf("%s: duplicate manifest entry", d.Label())
		}
		seen[d.Label()] = true

		if d.Kind == "" {
			t.Errorf("%s: no Kind", d.Label())
		}
		if d.Source == "" {
			t.Errorf("%s: no Source — provenance must be recorded", d.Label())
		}

		if d.ShipsCode() {
			if d.LicenseText == "" {
				t.Errorf("%s: ships code or artwork but declares no LicenseText", d.Label())
			}
			if d.SPDX == "" {
				t.Errorf("%s: ships code or artwork but declares no SPDX ID", d.Label())
			}
		} else {
			if d.LicenseText != "" {
				t.Errorf("%s: kind %q should not carry a license text", d.Label(), d.Kind)
			}
			if len(d.Files) != 0 {
				t.Errorf("%s: kind %q should not claim vendored files", d.Label(), d.Kind)
			}
			if d.Note == "" {
				t.Errorf("%s: kind %q must explain itself in Note", d.Label(), d.Kind)
			}
		}

		if d.Kind == KindAsset && len(d.Files) == 0 {
			t.Errorf("%s: declared as a vendored asset but claims no files", d.Label())
		}
	}
}

// TestDeclaredSPDXMatchesLicenseText is the core check: it reads the committed
// license text and asserts licensecheck agrees with the SPDX ID we claim. This
// is what makes the manifest evidence rather than an assertion — a wrong,
// swapped, or truncated license text fails here.
func TestDeclaredSPDXMatchesLicenseText(t *testing.T) {
	for _, d := range Deps {
		if !d.ShipsCode() {
			continue
		}
		t.Run(d.Label(), func(t *testing.T) {
			text, err := d.Text()
			if err != nil {
				t.Fatal(err)
			}
			cov := licensecheck.Scan([]byte(text))
			if cov.Percent < minCoverage {
				t.Fatalf("license text recognized at only %.0f%% (want >= %.0f%%); is texts/%s truncated or modified?",
					cov.Percent, minCoverage, d.LicenseText)
			}
			var got []string
			for _, m := range cov.Match {
				if m.ID == d.SPDX {
					return // declared ID confirmed by the text
				}
				got = append(got, m.ID)
			}
			t.Fatalf("declares %s but texts/%s reads as %v (coverage %.0f%%)",
				d.SPDX, d.LicenseText, got, cov.Percent)
		})
	}
}

// TestAllDependenciesArePermissive guards the licensing model: copyleft terms
// would constrain how MeshTender itself may be licensed and distributed, so no
// dependency may carry them.
func TestAllDependenciesArePermissive(t *testing.T) {
	for _, d := range Deps {
		if d.SPDX == "" {
			continue // services carry no license
		}
		if !AllowedSPDX[d.SPDX] {
			t.Errorf("%s declares %s, which is not on the permissive allowlist. "+
				"A copyleft dependency is a licensing conflict, not a build failure "+
				"to wave through.", d.Label(), d.SPDX)
		}
	}
}

// TestVendoredFilesMatchAuditedHashes pins each file to the content that was
// actually reviewed, so an upgrade cannot land without updating the manifest
// (and therefore without re-checking the license and version).
func TestVendoredFilesMatchAuditedHashes(t *testing.T) {
	root := repoRoot(t)
	for _, d := range Deps {
		for _, f := range d.Files {
			t.Run(f.Path, func(t *testing.T) {
				b, err := os.ReadFile(filepath.Join(root, f.Path))
				if err != nil {
					t.Fatalf("%s declares %s: %v", d.Label(), f.Path, err)
				}
				sum := sha256.Sum256(b)
				if got := hex.EncodeToString(sum[:]); got != f.SHA256 {
					t.Errorf("%s changed since it was audited for %s\n  want %s\n  got  %s\n"+
						"If this was an intentional upgrade, re-check the upstream license and "+
						"version, then run `mise run licenses --update`.", f.Path, d.Label(), f.SHA256, got)
				}
			})
		}
	}
}

// TestNoticeBearingFilesCarryAttribution enforces the actual legal obligation:
// MIT and the BSD licenses require the copyright notice travel with copies, and
// minifiers strip banners. Requiring the version string in the banner also
// catches a file swapped for a different release.
func TestNoticeBearingFilesCarryAttribution(t *testing.T) {
	const bannerWindow = 4096

	root := repoRoot(t)
	for _, d := range Deps {
		for _, f := range d.Files {
			if !f.Notice {
				continue
			}
			t.Run(f.Path, func(t *testing.T) {
				b, err := os.ReadFile(filepath.Join(root, f.Path))
				if err != nil {
					t.Fatal(err)
				}
				head := string(b)
				if len(head) > bannerWindow {
					head = head[:bannerWindow]
				}
				lower := strings.ToLower(head)

				if !strings.Contains(lower, "copyright") && !strings.Contains(lower, "(c)") {
					t.Errorf("%s carries no copyright notice in its first %d bytes; %s is %s and "+
						"requires the notice be retained in redistributed copies",
						f.Path, bannerWindow, d.Label(), d.SPDX)
				}
				if !strings.Contains(lower, strings.ToLower(d.Name)) {
					t.Errorf("%s has a banner that does not name %q", f.Path, d.Name)
				}
				if d.Version != "" && !strings.Contains(head, d.Version) {
					t.Errorf("%s has a banner that does not state version %s; either the banner is "+
						"stale or the file was upgraded without updating the manifest", f.Path, d.Version)
				}
			})
		}
	}
}

// TestEveryStaticFileIsAccountedFor is the check that keeps this manifest
// honest over time. Without it, the next vendored library is simply absent from
// the audit and nothing notices.
func TestEveryStaticFileIsAccountedFor(t *testing.T) {
	root := repoRoot(t)
	staticDir := filepath.Join(root, "internal", "web", "static")

	claimed := map[string]string{} // base name -> owning dependency
	for _, d := range Deps {
		for _, f := range d.Files {
			claimed[filepath.Base(f.Path)] = d.Label()
		}
	}
	firstParty := map[string]bool{}
	for _, name := range FirstPartyStatic {
		firstParty[name] = true
	}
	// Brand artwork is first-party too, but under reserved terms rather than the
	// AGPL — a third bucket, not an exception to the rule.
	brand := map[string]bool{}
	for _, a := range BrandAssets {
		if dir, name := filepath.Split(a.Path); filepath.Clean(dir) == filepath.Join("internal", "web", "static") {
			brand[name] = true
		}
	}

	entries, err := os.ReadDir(staticDir)
	if err != nil {
		t.Fatalf("reading %s: %v", staticDir, err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		switch {
		case claimed[name] != "":
			// vendored and declared
		case firstParty[name]:
			// ours, under our own license
		case brand[name]:
			// ours, under the reserved terms in TRADEMARKS.md
		default:
			t.Errorf("internal/web/static/%s is declared nowhere in internal/licenses/manifest.go. "+
				"If it is third-party, add a Deps entry with its license; if we wrote it, add it to "+
				"FirstPartyStatic; if it carries the MeshTender name or mark, add it to BrandAssets.", name)
		}
	}

	// A stale FirstPartyStatic entry is worth knowing about too: it means a file
	// was deleted or renamed and the list drifted.
	present := map[string]bool{}
	for _, e := range entries {
		present[e.Name()] = true
	}
	for _, name := range FirstPartyStatic {
		if !present[name] {
			t.Errorf("FirstPartyStatic lists %q, which no longer exists in internal/web/static", name)
		}
	}
}

// TestNoticesFileIsCurrent proves the published notices file still matches the
// manifest. Only the assets half is checked here: the Go-module half needs the
// module cache, so `mise run licenses` (and the CI step running it) owns that.
func TestNoticesFileIsCurrent(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, NoticesPath)

	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v (run `mise run licenses --update`)", NoticesPath, err)
	}

	got, err := Section(string(doc), AssetsBegin, AssetsEnd)
	if err != nil {
		t.Fatalf("%s: %v", NoticesPath, err)
	}
	want, err := AssetsSection()
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.TrimSpace(want) {
		t.Errorf("%s is out of date with internal/licenses/manifest.go — run `mise run licenses --update`", NoticesPath)
	}

	goSection, err := Section(string(doc), GoBegin, GoEnd)
	if err != nil {
		t.Fatalf("%s: %v", NoticesPath, err)
	}
	if goSection == "" {
		t.Errorf("%s has an empty Go module section — run `mise run licenses --update`", NoticesPath)
	}
}

// TestNoticesUsesUnixLineEndings is a regression test. Leaflet's license file
// ships with CRLF, and the generator used to copy those bytes straight into the
// Markdown. The committed file then got normalized to LF by an editor, so it no
// longer matched the generator's output and TestNoticesFileIsCurrent failed on
// every run with no way to fix it by regenerating. A mixed-line-ending
// generated file is a trap; assert it never comes back.
func TestNoticesUsesUnixLineEndings(t *testing.T) {
	assets, err := AssetsSection()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(assets, "\r") {
		t.Error("AssetsSection emits a carriage return; embedded license text must be normalized to LF before it reaches the Markdown")
	}

	doc, err := os.ReadFile(filepath.Join(repoRoot(t), NoticesPath))
	if err != nil {
		t.Fatalf("reading %s: %v", NoticesPath, err)
	}
	if bytes.Contains(doc, []byte("\r")) {
		t.Errorf("%s contains a carriage return — run `mise run licenses --update`", NoticesPath)
	}
}
