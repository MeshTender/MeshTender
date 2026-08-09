package licenses

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The trademark carve-out in TRADEMARKS.md is prose, and prose does not survive
// refactors. These tests are what make it hold: the brand files stay where
// TRADEMARKS.md says they are, they still contain the artwork that was reserved,
// they carry a notice saying so where anyone editing them will read it, and
// TRADEMARKS.md keeps naming them so a fork can find what it has to remove.

// brandNoticeWindow is how far into a file the reservation notice must appear.
// Generous enough for a template comment block, small enough that the notice has
// to be at the top rather than buried after the artwork.
const brandNoticeWindow = 2048

// TestBrandAssetsExistAndMatchHashes pins the reserved artwork. A change here is
// never routine: it means the mark itself moved, so the notice, TRADEMARKS.md,
// and the reproducibility story all want re-reading before the hash is updated.
func TestBrandAssetsExistAndMatchHashes(t *testing.T) {
	root := repoRoot(t)
	if len(BrandAssets) == 0 {
		t.Fatal("BrandAssets is empty; the trademark carve-out in TRADEMARKS.md covers specific files, so this list should never be")
	}
	for _, a := range BrandAssets {
		t.Run(a.Path, func(t *testing.T) {
			if a.Desc == "" {
				t.Errorf("%s has no Desc", a.Path)
			}
			b, err := os.ReadFile(filepath.Join(root, a.Path))
			if err != nil {
				t.Fatalf("%s is declared as brand artwork but cannot be read: %v\n"+
					"If it moved, update BrandAssets and TRADEMARKS.md together — TRADEMARKS.md "+
					"names these paths so a fork knows what to remove.", a.Path, err)
			}
			sum := sha256.Sum256(b)
			if got := hex.EncodeToString(sum[:]); got != a.SHA256 {
				t.Errorf("%s changed\n  want %s\n  got  %s\n"+
					"This is reserved artwork, not a vendored file: re-read the notice it carries "+
					"and TRADEMARKS.md before updating the hash.", a.Path, a.SHA256, got)
			}
		})
	}
}

// TestBrandAssetsCarryReservationNotice is the check that keeps the carve-out
// discoverable. Nothing about an SVG announces that it sits outside the AGPL
// grant, so someone copying the mark into a new file — or a fork assuming the
// whole tree is AGPL — has to be told in the file itself.
func TestBrandAssetsCarryReservationNotice(t *testing.T) {
	root := repoRoot(t)
	for _, a := range BrandAssets {
		t.Run(a.Path, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(root, a.Path))
			if err != nil {
				t.Fatal(err)
			}
			head := string(b)
			if len(head) > brandNoticeWindow {
				head = head[:brandNoticeWindow]
			}
			lower := strings.ToLower(head)

			for _, want := range []struct{ needle, why string }{
				{"trademarks.md", "point the reader at the policy that governs it"},
				{"reserved", "say that rights in the mark are reserved"},
				{"copyright", "carry a copyright line"},
			} {
				if !strings.Contains(lower, want.needle) {
					t.Errorf("%s: no %q in its first %d bytes; brand artwork must %s",
						a.Path, want.needle, brandNoticeWindow, want.why)
				}
			}
		})
	}
}

// TestTrademarkPolicyNamesEveryBrandAsset closes the loop the other way. The
// obligation TRADEMARKS.md places on a fork ("remove these files") is only
// followable if the list there is complete, and a list maintained by hand in a
// markdown file is exactly the kind that goes quietly stale.
func TestTrademarkPolicyNamesEveryBrandAsset(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "TRADEMARKS.md"))
	if err != nil {
		t.Fatalf("reading TRADEMARKS.md: %v", err)
	}
	policy := string(b)

	for _, a := range BrandAssets {
		if !strings.Contains(policy, a.Path) {
			t.Errorf("TRADEMARKS.md does not name %s. A fork is told to remove the brand "+
				"artwork, so every path in BrandAssets has to be listed there.", a.Path)
		}
	}
}

// TestBrandAssetsAreNotClaimedAsThirdParty guards against the mark being filed
// in the wrong bucket. Deps entries are third-party work we redistribute under
// someone else's permissive license; the mark is ours and is redistributable by
// nobody. Confusing the two would advertise the opposite of the carve-out.
func TestBrandAssetsAreNotClaimedAsThirdParty(t *testing.T) {
	brand := map[string]string{}
	for _, a := range BrandAssets {
		brand[a.Path] = a.Desc
	}
	for _, d := range Deps {
		for _, f := range d.Files {
			if _, ok := brand[f.Path]; ok {
				t.Errorf("%s is listed in BrandAssets but also claimed by %s as third-party. "+
					"The MeshTender mark is first-party reserved artwork, not a vendored dependency.",
					f.Path, d.Label())
			}
		}
	}
	for _, name := range FirstPartyStatic {
		if _, ok := brand[filepath.Join("internal", "web", "static", name)]; ok {
			t.Errorf("internal/web/static/%s is in both FirstPartyStatic and BrandAssets. "+
				"FirstPartyStatic files ship under MeshTender's own AGPL license; brand artwork "+
				"does not, so it belongs in exactly one of the two.", name)
		}
	}
}
