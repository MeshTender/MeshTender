package licenses

import (
	"fmt"
	"strings"
)

// NoticesPath is where the generated notices file lives, relative to the
// repository root.
const NoticesPath = "THIRD-PARTY-NOTICES.md"

// The generated file is split into two marked sections so each has an owner.
// The assets section derives entirely from this package's manifest, so the
// tests can regenerate and compare it with no network and no module cache. The
// Go-module section needs `go list` plus the module cache, so cmd/licenses owns
// it and CI is what proves it current.
const (
	AssetsBegin = "<!-- BEGIN GENERATED: assets — edit internal/licenses/manifest.go, then run `mise run licenses --update` -->"
	AssetsEnd   = "<!-- END GENERATED: assets -->"
	GoBegin     = "<!-- BEGIN GENERATED: go-modules — run `mise run licenses --update` -->"
	GoEnd       = "<!-- END GENERATED: go-modules -->"
)

// Section extracts the text between two markers, exclusive of the markers.
func Section(doc, begin, end string) (string, error) {
	i := strings.Index(doc, begin)
	if i < 0 {
		return "", fmt.Errorf("licenses: marker %q not found", begin)
	}
	i += len(begin)
	j := strings.Index(doc[i:], end)
	if j < 0 {
		return "", fmt.Errorf("licenses: marker %q not found after %q", end, begin)
	}
	return strings.TrimSpace(doc[i : i+j]), nil
}

// AssetsSection renders the manifest as Markdown. It is a pure function of
// Deps, which is what lets the test assert the committed file matches.
func AssetsSection() (string, error) {
	var b strings.Builder

	shipping, referenced := splitByKind(Deps)

	b.WriteString("## Vendored front-end assets and artwork\n\n")
	b.WriteString("The following third-party code and artwork is redistributed as part of\n")
	b.WriteString("MeshTender — compiled into the binary via `go:embed` and served to browsers.\n")

	for _, d := range shipping {
		if err := writeDep(&b, d, true); err != nil {
			return "", err
		}
	}

	b.WriteString("\n## Base image and external services\n\n")
	b.WriteString("These are not compiled into the binary. The base image is redistributed as\n")
	b.WriteString("part of the published container; the service is called by the browser at runtime.\n")

	for _, d := range referenced {
		if err := writeDep(&b, d, false); err != nil {
			return "", err
		}
	}

	return strings.TrimSpace(b.String()), nil
}

func splitByKind(deps []Dep) (shipping, referenced []Dep) {
	for _, d := range deps {
		if d.ShipsCode() {
			shipping = append(shipping, d)
		} else {
			referenced = append(referenced, d)
		}
	}
	return shipping, referenced
}

func writeDep(b *strings.Builder, d Dep, withText bool) error {
	fmt.Fprintf(b, "\n### %s", d.Label())
	if d.SPDX != "" {
		fmt.Fprintf(b, " — %s", d.SPDX)
	}
	b.WriteString("\n\n")

	if d.Homepage != "" {
		fmt.Fprintf(b, "- Homepage: <%s>\n", d.Homepage)
	}
	if d.Source != "" {
		fmt.Fprintf(b, "- Source: %s\n", d.Source)
	}
	for _, f := range d.Files {
		fmt.Fprintf(b, "- File: `%s` (sha256 `%s`)\n", f.Path, f.SHA256)
	}
	if d.Modified != "" {
		fmt.Fprintf(b, "- Modified: %s\n", d.Modified)
	}
	if d.Note != "" {
		fmt.Fprintf(b, "- Note: %s\n", d.Note)
	}

	if !withText {
		return nil
	}
	text, err := d.Text()
	if err != nil {
		return err
	}
	b.WriteString("\n```\n")
	b.WriteString(normalizeEOL(strings.TrimSpace(text)))
	b.WriteString("\n```\n")
	return nil
}

// normalizeEOL rewrites CRLF to LF. Some upstream license files ship with
// Windows line endings (Leaflet's does), and Dep.Text deliberately returns them
// verbatim so the SPDX check reads exactly what upstream published. Writing
// those bytes straight into the Markdown, though, leaves the generated file
// with mixed line endings — which any editor or `git add` with autocrlf will
// silently normalize, permanently desynchronizing the committed file from what
// the generator produces and failing the drift check forever. That already
// happened once. Normalizing here keeps the notices file pure LF, while the
// embedded texts/ files stay byte-faithful to upstream.
func normalizeEOL(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// GoModule is one module from the Go dependency graph, as scanned by
// cmd/licenses.
type GoModule struct {
	Path       string
	Version    string
	SPDX       string
	Copyrights []string
	// InBinary distinguishes modules linked into the shipped binary from those
	// only used by tests and tooling. Both are listed; only the first group
	// carries redistribution obligations.
	InBinary bool
}

// GoSection renders scanned modules as Markdown, grouped by whether they ship.
func GoSection(mods []GoModule) string {
	var b strings.Builder

	b.WriteString("Go module dependencies, scanned from the module graph with\n")
	b.WriteString("[licensecheck](https://github.com/google/licensecheck). Each module's own\n")
	b.WriteString("license file is the authoritative text; the copyright lines below are\n")
	b.WriteString("reproduced from it to satisfy the attribution clauses.\n")

	groups := []struct {
		title    string
		blurb    string
		inBinary bool
	}{
		{
			"Linked into the MeshTender binary",
			"Redistributed in compiled form. Their notices are reproduced here.",
			true,
		},
		{
			"Build, test, and tooling only",
			"Not present in the shipped binary or container. Listed for completeness.",
			false,
		},
	}

	for _, g := range groups {
		var rows []GoModule
		for _, m := range mods {
			if m.InBinary == g.inBinary {
				rows = append(rows, m)
			}
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n\n%s\n\n", g.title, g.blurb)
		for _, m := range rows {
			fmt.Fprintf(&b, "- **%s** %s — %s", m.Path, m.Version, m.SPDX)
			if len(m.Copyrights) > 0 {
				fmt.Fprintf(&b, "  \n  %s", strings.Join(m.Copyrights, "  \n  "))
			}
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
}

// Notices assembles the whole file from the manifest and a scanned Go section.
// Passing an empty goSection preserves nothing — callers that only want to
// refresh the assets half should read the existing file and pass its Go section
// back in.
func Notices(goSection string) (string, error) {
	assets, err := AssetsSection()
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# Third-Party Notices\n\n")
	b.WriteString("This file covers the third-party software MeshTender depends on, all of\n")
	b.WriteString("which is permissively licensed. It makes no statement about MeshTender's\n")
	b.WriteString("own license.\n\n")
	b.WriteString("**This file is generated. Do not edit it by hand** — run `mise run licenses --update`.\n")
	b.WriteString("Front-end and artwork entries come from `internal/licenses/manifest.go`; the Go\n")
	b.WriteString("module list is scanned from the module graph.\n\n")

	b.WriteString(AssetsBegin + "\n\n")
	b.WriteString(assets + "\n\n")
	b.WriteString(AssetsEnd + "\n\n")

	b.WriteString("## Go modules\n\n")
	b.WriteString(GoBegin + "\n\n")
	b.WriteString(strings.TrimSpace(goSection) + "\n\n")
	b.WriteString(GoEnd + "\n")

	return b.String(), nil
}
