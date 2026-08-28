// Package licenses is the manifest of every third-party dependency that Go
// tooling cannot see: the front-end libraries vendored into
// internal/web/static, third-party code bundled inside those libraries, icon
// artwork copied into our templates, the container base image, and the one
// external service the app talks to at runtime.
//
// Go modules are deliberately NOT listed here — the module graph already names
// them and every module ships its own license file. `mise run licenses` scans
// them and writes their section of the generated THIRD-PARTY-NOTICES.md.
//
// The tests in this package are what make the manifest load-bearing rather than
// documentation: they verify each declared SPDX ID against the committed
// license text (via github.com/google/licensecheck), that every declared file
// still hashes to what was audited, that files needing an attribution notice
// carry one, and — most importantly — that nothing third-party in
// internal/web/static escapes the manifest entirely.
package licenses

import (
	"embed"
	"fmt"
)

// texts holds the verbatim upstream license text for each dependency. Fetch
// these with `mise run licenses --refresh`; never hand-edit them, because the
// tests scan them to confirm the declared SPDX ID is really what the text says.
//
//go:embed texts/*.txt
var texts embed.FS

// Kind describes what sort of dependency an entry is, which decides which
// checks apply to it.
type Kind string

const (
	// KindAsset is a third-party file served to the browser out of
	// internal/web/static.
	KindAsset Kind = "asset"

	// KindBundled is third-party code embedded inside another vendored asset.
	// It has no file of its own, so only its license text is checked — but it
	// still ships to users and still needs attribution.
	KindBundled Kind = "bundled"

	// KindArtwork is third-party art (icon paths) copied into our templates
	// rather than vendored as a whole file.
	KindArtwork Kind = "artwork"

	// KindImage is a container base image referenced by the ko build config
	// (.ko.yaml). We redistribute it as part of the published image.
	KindImage Kind = "image"

	// KindService is an external service the app calls at runtime. It ships no
	// code, so there is no license to scan — only terms to point at.
	KindService Kind = "service"
)

// File is one vendored file, pinned to the content that was audited.
type File struct {
	// Path is relative to the repository root.
	Path string

	// SHA256 is the hash of the file as committed. For most files that is
	// byte-identical to the upstream artifact; where our vendoring process
	// modifies it (a restored banner, a stripped sourceMappingURL comment, or
	// two upstream stylesheets concatenated), Dep.Modified says so.
	SHA256 string

	// Notice marks a file that must carry a copyright banner naming the
	// dependency, because its license requires the notice travel with copies.
	Notice bool
}

// Dep is one third-party dependency outside the Go module graph.
type Dep struct {
	Name     string
	Version  string // empty when upstream publishes no version we can pin
	SPDX     string // must agree with what licensecheck reads from LicenseText
	Homepage string
	Kind     Kind

	// Source is where the artifact came from, so provenance is a fact in the
	// repo rather than an investigation later.
	Source string

	// LicenseText names a file in texts/. Required for everything that ships
	// code or art; empty for images and services.
	LicenseText string

	// Files are the vendored files this dependency accounts for. Empty for
	// bundled code, images, and services.
	Files []File

	// Modified explains how the committed files differ from upstream, or is
	// empty when they are byte-identical.
	Modified string

	// Note carries anything a future reader needs: why an entry exists, what
	// terms apply, what to watch out for.
	Note string
}

// Deps is the manifest. Adding a vendored library means adding it here — the
// tests fail otherwise.
var Deps = []Dep{
	{
		Name:        "htmx",
		Version:     "2.0.10",
		SPDX:        "0BSD",
		Homepage:    "https://htmx.org",
		Kind:        KindAsset,
		Source:      "https://cdn.jsdelivr.net/npm/htmx.org@2.0.10/dist/htmx.min.js",
		LicenseText: "htmx-2.0.10.txt",
		// 0BSD imposes no attribution requirement at all, so Notice is false:
		// the upstream build ships no banner and none is owed.
		Files: []File{
			{Path: "internal/web/static/htmx.min.js", SHA256: "71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de"},
		},
	},
	{
		Name:        "MapLibre GL JS",
		Version:     "5.24.0",
		SPDX:        "BSD-3-Clause",
		Homepage:    "https://maplibre.org",
		Kind:        KindAsset,
		Source:      "https://cdn.jsdelivr.net/npm/maplibre-gl@5.24.0/dist/",
		LicenseText: "maplibre-gl-5.24.0.txt",
		Files: []File{
			{Path: "internal/web/static/maplibre-gl.js", SHA256: "039b46f7a84489bce207f55cf376e1022cf8d213190ff9a93b02554f1785248f", Notice: true},
			{Path: "internal/web/static/maplibre-gl-worker.js", SHA256: "4f64bf26dab4a953ceb06c49a8d6efef32d5015a3f4c872a1f73e13f60ac9dba", Notice: true},
			{Path: "internal/web/static/maplibre-gl.css", SHA256: "3f40ab71b5b3fc985eb7f6b8926e0e8fbec2792852e4f12d8247abf0aabb8a94", Notice: true},
		},
		Modified: "All three carry hand-extended or hand-restored @preserve banners (upstream's script banner names the license by URL and states no copyright; the stylesheet ships none) and have their trailing sourceMappingURL comment stripped, since the .map files are not vendored. Bodies are byte-identical to upstream.",
		Note:     "This is the CSP build: maplibre-gl.js is upstream dist/maplibre-gl-csp.js and maplibre-gl-worker.js is dist/maplibre-gl-csp-worker.js. The default bundle spawns its worker from a blob: URL, which the strict CSP forbids; the CSP build takes an explicit same-origin worker URL via maplibregl.setWorkerUrl instead (see basemap.js). Upstream's LICENSE.txt, reproduced whole in the notices, also covers the third-party code MapLibre embeds — mapbox-gl-js v1.13 and earlier (BSD-3-Clause, (c) 2020 Mapbox), glfx.js (MIT) and a portion of d3-color (BSD-3-Clause) — all permissive.",
	},
	{
		Name:        "Terra Draw",
		Version:     "1.32.3",
		SPDX:        "MIT",
		Homepage:    "https://github.com/JamesLMilner/terra-draw",
		Kind:        KindAsset,
		Source:      "https://cdn.jsdelivr.net/npm/terra-draw@1.32.3/dist/terra-draw.umd.js",
		LicenseText: "terra-draw-1.32.3.txt",
		Files: []File{
			{Path: "internal/web/static/terra-draw.js", SHA256: "1d6fcaf87f34ec1515e7f960d39915be83365e1c521bdc10e64e25071df7a18d", Notice: true},
		},
		Modified: "Hand-restored banner (the upstream UMD bundle ships none) and a stripped trailing sourceMappingURL comment. Body is byte-identical to upstream.",
		Note:     "Drives polygon drawing and editing on the region area editor, replacing Leaflet-Geoman. Headless by design — it renders shapes into the map and ships no toolbar UI, so the controls are ours (see regionmap.js and config_region_area.html). The npm package ships no LICENSE file; the text here is the one at the root of the monorepo that publishes it, which the adapter shares.",
	},
	{
		Name:        "Terra Draw MapLibre GL adapter",
		Version:     "1.4.1",
		SPDX:        "MIT",
		Homepage:    "https://github.com/JamesLMilner/terra-draw",
		Kind:        KindAsset,
		Source:      "https://cdn.jsdelivr.net/npm/terra-draw-maplibre-gl-adapter@1.4.1/dist/terra-draw-maplibre-gl-adapter.umd.js",
		LicenseText: "terra-draw-maplibre-gl-adapter-1.4.1.txt",
		Files: []File{
			{Path: "internal/web/static/terra-draw-maplibre-gl-adapter.js", SHA256: "ac90d8efe1d367e483e11decdffc421a7150d57686aec016b261f3f32e49bec7", Notice: true},
		},
		Modified: "Hand-restored banner (the upstream UMD bundle ships none) and a stripped trailing sourceMappingURL comment. Body is byte-identical to upstream.",
		Note:     "Published from the same monorepo as Terra Draw and under the same root MIT license, which is why the two license texts here are identical.",
	},
	{
		Name:        "Tabler",
		Version:     "1.4.0",
		SPDX:        "MIT",
		Homepage:    "https://tabler.io",
		Kind:        KindAsset,
		Source:      "https://cdn.jsdelivr.net/npm/@tabler/core@1.4.0/dist/",
		LicenseText: "tabler-1.4.0.txt",
		Files: []File{
			{Path: "internal/web/static/tabler.min.js", SHA256: "b60c76160e97624574dbb8cf10abe6aee9a6493b60096fdfc15dd1dd2bd99eb9", Notice: true},
			{Path: "internal/web/static/tabler.min.css", SHA256: "7ef750bd10546a695d0b12767ad8048bd8f3ec5de7daefb1067f9d0daa3d1c9a", Notice: true},
		},
		Note: "These two files are byte-identical to the public MIT @tabler/core@1.4.0 npm artifacts, verified by SHA-256. Tabler's paid add-ons (Illustrations, Emails, Avatars) are a Personal License that forbids open-source redistribution — nothing from them may enter this repository. The license text here is from the tabler/tabler dev branch: upstream publishes no v1.4.0 git tag and the npm package ships no LICENSE file.",
	},
	{
		Name:        "Bootstrap",
		Version:     "5.3.7",
		SPDX:        "MIT",
		Homepage:    "https://getbootstrap.com",
		Kind:        KindBundled,
		Source:      "bundled inside @tabler/core@1.4.0 dist/js/tabler.min.js",
		LicenseText: "bootstrap-5.3.7.txt",
		Note:        "Not vendored directly: Tabler's bundle embeds Bootstrap, which its own banner declares partway through tabler.min.js. It ships to every user, so it is attributed here.",
	},
	{
		Name:        "Tabler Icons",
		SPDX:        "MIT",
		Homepage:    "https://tabler.io/icons",
		Kind:        KindArtwork,
		Source:      "https://github.com/tabler/tabler-icons (icon path data, various versions)",
		LicenseText: "tabler-icons.txt",
		Files: []File{
			{Path: "internal/web/templates/icons.html", SHA256: "2f23e95b43f6c6bba164f7c470ddc5f69d5376f20b088dc919b74a27dd74e20e", Notice: true},
		},
		Modified: "Icon path data copied into Go template definitions rather than vendored as SVG files; the transparent 24x24 guard path upstream emits is dropped.",
		Note:     "All 45 icons in icons.html are Tabler Icons; several are renamed locally (antenna<-antenna-bars-5, copy<-squares, list<-list-details, plug<-plug-connected, terminal<-terminal-2, alert<-alert-triangle, brand-signal<-message-circle-2). Version is unpinned because the set was collected across releases.",
	},
	{
		Name:     "distroless static-debian12",
		SPDX:     "Apache-2.0",
		Homepage: "https://github.com/GoogleContainerTools/distroless",
		Kind:     KindImage,
		Source:   "gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab (defaultBaseImage in .ko.yaml)",
		Note:     "Runtime base image, redistributed as part of the published container. The distroless project is Apache-2.0; the image layer also carries Debian-packaged CA certificates and tzdata under their own upstream licenses (Mozilla's CA bundle is MPL-2.0, applying to the certificate data we redistribute unmodified, not to MeshTender).",
	},
	{
		Name:     "CARTO basemaps",
		Homepage: "https://carto.com",
		Kind:     KindService,
		Source:   "https://*.basemaps.cartocdn.com (allowlisted in the CSP connect-src), keyed with MESHTENDER_CARTO_KEY",
		Note:     "Vector basemaps (MapLibre GL styles) rendered in the browser at runtime; no code is redistributed, so no license applies. A style pulls four kinds of resource from CARTO — the style JSON, .mvt tiles, a sprite sheet and glyph PBFs — all fetched rather than loaded as images, which is why the CSP allowlists the host in connect-src rather than img-src. Attribution (\"(c) OpenStreetMap (c) CARTO\") is rendered by basemap.js, and CARTO require it stay visible. Every request carries an API key (MESHTENDER_CARTO_KEY, per deployment) appended by a MapLibre transformRequest, because the URLs inside a style do not carry one. The raster tiles this replaced are deprecated upstream. Terms of use are CARTO's and are not verified by any test.",
	},
}

// BrandAsset is a first-party file carrying the MeshTender name or mark, which
// TRADEMARKS.md excludes from the AGPL grant covering the rest of the tree.
//
// These files are committed rather than injected at deploy time on purpose: the
// published image is verifiable only if every input to it is public (see
// "Verifying a build" in README.md), so the trademark boundary has to be legal
// rather than physical. AGPLv3 section 7 permits exactly that — 7(e) for
// declining a trademark grant, 7(c) for requiring modified versions be marked as
// different.
//
// Because nothing about a committed SVG announces that it is carved out, the
// carve-out is only real if the notice stays attached to it. Hence the checks in
// this package: each file must exist, must still hash to what is declared, and
// must carry a reservation notice pointing at TRADEMARKS.md.
type BrandAsset struct {
	// Path is relative to the repository root.
	Path string

	// SHA256 pins the artwork as committed. A changed mark means the notice and
	// TRADEMARKS.md need re-reading, not a hash bump reflexively.
	SHA256 string

	// What the file is, for the failure message a future reader will see.
	Desc string
}

// BrandAssets is every file the trademark carve-out covers. Adding brand artwork
// means adding it here and to TRADEMARKS.md, which names these paths so a fork
// knows exactly what to remove.
var BrandAssets = []BrandAsset{
	{
		Path:   "internal/web/templates/brand.html",
		SHA256: "1ec0ddba1a515e6f46cdff85e5fff82e68373f5fd375a9b6b7cc3e6e04a99df9",
		Desc:   "the icon-logo brand mark, rendered in the page chrome",
	},
	{
		Path:   "internal/web/static/favicon.svg",
		SHA256: "5d4f195212e4770844ba077d6c62074be23a51bfaaf9049a0cce22ea4b526a58",
		Desc:   "the favicon form of the brand mark",
	},
}

// FirstPartyStatic lists the files in internal/web/static that we wrote
// ourselves and that ship under MeshTender's own AGPL license. Anything in that
// directory which is not listed here, claimed by a Deps entry, or listed in
// BrandAssets fails TestEveryStaticFileIsAccountedFor — that check is what stops
// the next vendored library from slipping in unaudited.
var FirstPartyStatic = []string{
	"app.css",
	"basemap.js",
	"console-config.js",
	"console.js",
	"link-editor.js",
	"listfilter.js",
	"meshmap.js",
	"regionmap.js",
	"serial-port.js",
	"serial-setup.js",
	"timezone-picker.js",
	"ui.js",
	"webauthn.js",
}

// AllowedSPDX is the set of licenses a dependency may carry. Everything here is
// permissive, and it stays that way even though MeshTender is itself AGPL-3.0:
// licensing copyleft OUT does not oblige us to accept copyleft IN, and the two
// reasons to keep the inbound side permissive both still hold.
//
// First, relicensing latitude. The copyright in MeshTender is held by one person,
// who can therefore release it under different or additional terms later (a
// dual-license, say). A GPL or AGPL dependency compiled into the binary would end
// that: the combined work could only ever be distributed under those terms, no
// matter what the copyright holder wanted.
//
// Second, per-file obligations that a single static binary handles badly. LGPL and
// MPL attach conditions to their own files rather than to the whole work — LGPL
// wants the user able to relink against a modified version, MPL wants modified
// covered files published under MPL — and neither is a natural fit for a pure-Go
// binary with everything go:embed'ed and no dynamic linking.
//
// Adding to this list is a legal decision, not a build fix.
var AllowedSPDX = map[string]bool{
	"0BSD":         true,
	"Apache-2.0":   true,
	"BSD-2-Clause": true,
	"BSD-3-Clause": true,
	"ISC":          true,
	"MIT":          true,
	"Unlicense":    true,
}

// LicenseText returns the verbatim upstream license text for a dependency.
func (d Dep) Text() (string, error) {
	if d.LicenseText == "" {
		return "", fmt.Errorf("licenses: %s declares no license text", d.Name)
	}
	b, err := texts.ReadFile("texts/" + d.LicenseText)
	if err != nil {
		return "", fmt.Errorf("licenses: reading text for %s: %w", d.Name, err)
	}
	return string(b), nil
}

// Label names a dependency for humans, with its version when we have one.
func (d Dep) Label() string {
	if d.Version == "" {
		return d.Name
	}
	return d.Name + " " + d.Version
}

// ShipsCode reports whether an entry carries code or art we redistribute, and
// therefore must have a scannable license text.
func (d Dep) ShipsCode() bool {
	switch d.Kind {
	case KindAsset, KindBundled, KindArtwork:
		return true
	default:
		return false
	}
}
