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
		Name:        "esp-web-tools",
		Version:     "10.5.2",
		SPDX:        "Apache-2.0",
		Homepage:    "https://github.com/esphome/esp-web-tools",
		Kind:        KindAsset,
		Source:      "https://registry.npmjs.org/esp-web-tools/-/esp-web-tools-10.5.2.tgz (dist/web/)",
		LicenseText: "esp-web-tools-10.5.2.txt",
		Files: []File{
			{Path: "internal/web/static/esp-web-tools/cdc-acm-CgpHa-zp.js", SHA256: "7ea3e4b2f23c41f248ea6e95655c32ec6c3365c4171bd51c4b51f44c1af790af", Notice: true},
			{Path: "internal/web/static/esp-web-tools/ch34x-O7hnS6Rn.js", SHA256: "2e636731fb86534111ef4925c43ac952cd45e79c5f1cfddb32b7dc0859d099da", Notice: true},
			{Path: "internal/web/static/esp-web-tools/cp210x-CD3hAcBr.js", SHA256: "ed515b542259138bd38767d00229ae5598ebb8b8e16e3840075613ed3e19c6f3", Notice: true},
			{Path: "internal/web/static/esp-web-tools/esp32-BlyKoTgy.js", SHA256: "8f83d5c7ea8350ac23d98e8ceb022c9631e25587c7d9f2f798b90e5fbb92510c", Notice: true},
			{Path: "internal/web/static/esp-web-tools/esp32c2-Bmu-jpVE.js", SHA256: "62255873404fcaf0b19875282d6caae359bc8da7d1b8dde905e3a3238018ec90", Notice: true},
			{Path: "internal/web/static/esp-web-tools/esp32c3-81Rr-aQE.js", SHA256: "19ece1457c26d29d8991d59693633167a7a137114a1dea4967d624b4e0edd58b", Notice: true},
			{Path: "internal/web/static/esp-web-tools/esp32c5-Bq6mXcb5.js", SHA256: "c9655ee986616e0d6fb692aa703843ae95d438dd5bb7ad61e6a6f0446e502862", Notice: true},
			{Path: "internal/web/static/esp-web-tools/esp32c6-yQoBKrxY.js", SHA256: "74b0d1f8e30c4db9a86f7439ef12a8e70b00989feef22011ba24c9ada3096ec2", Notice: true},
			{Path: "internal/web/static/esp-web-tools/esp32c61-DmDn041x.js", SHA256: "ff1ec4a6307d66240e36e7ce57fc09ef44b809e0851b436afd7c453e7eb2701c", Notice: true},
			{Path: "internal/web/static/esp-web-tools/esp32h2-B8bZhHv5.js", SHA256: "f84ea8b588ca7b095c40b369dd20e629f59e22f3a19f2083434247c044cdfe10", Notice: true},
			{Path: "internal/web/static/esp-web-tools/esp32h4-KyNsS-mo.js", SHA256: "870357b7fe5f78aba2b1e38fe06f99593951d4425bfd2ef6e92b46df3f19a403", Notice: true},
			{Path: "internal/web/static/esp-web-tools/esp32p4-o74i42i3.js", SHA256: "90387d53659c1fbcb9eabf421af43318ab812202145bb96dbe1732dc64f4e155", Notice: true},
			{Path: "internal/web/static/esp-web-tools/esp32p4-rev1-DEdUM_9Q.js", SHA256: "ed7f0c9e5ffab01004e167caac3014d6d701a07949cfeb0fade23563a87f89c0", Notice: true},
			{Path: "internal/web/static/esp-web-tools/esp32s2-O1Tyu6IT.js", SHA256: "d9d5fd8bea8b42b3593651c3503d46bbcc7077e2251606aa2e67a6daf9ebb5a0", Notice: true},
			{Path: "internal/web/static/esp-web-tools/esp32s3-WXxMoV8x.js", SHA256: "5adb4f5ac9fc7872d02573b40c6f3da44094e07acbb95d6fe45516680faf956b", Notice: true},
			{Path: "internal/web/static/esp-web-tools/esp32s31-Ef8NLP2_.js", SHA256: "fe69e34305ff3d2db13951134734c399e96a39e508f497bb072e04f061ef5fd1", Notice: true},
			{Path: "internal/web/static/esp-web-tools/esp8266-DsDItw_h.js", SHA256: "b9977ac1b5e77e88658ba36707a508db844a9994a485030ef3b647367ea2f6dc", Notice: true},
			{Path: "internal/web/static/esp-web-tools/ftdi-E6n0R6W9.js", SHA256: "e1dbcb1d42d456bea06d85bd3de570ca091bb53960ea1859050dd7dd5dd53bd8", Notice: true},
			{Path: "internal/web/static/esp-web-tools/index-rScOpSrP.js", SHA256: "ef774e2e434f4d4bdde04e91aeb61802428f095e11b6a9f4a6b24b20bf049cb5", Notice: true},
			{Path: "internal/web/static/esp-web-tools/install-button.js", SHA256: "fe7632d130e95ecdbb5a890a744299dce5c77139b0160bcdd6eb3b3219e32a23", Notice: true},
			{Path: "internal/web/static/esp-web-tools/install-dialog-ChRWE6GU.js", SHA256: "1faf7fab9a051ca0d4d648c6c8604ccbb6c13cd89ce9f1c1c2a26f3b61f0d817", Notice: true},
			{Path: "internal/web/static/esp-web-tools/no-port-picked-DPUnbpty.js", SHA256: "565b85439ce3ec6ae1732b69304f9d3478d73f7076e97d36bd66500a866f6363", Notice: true},
			{Path: "internal/web/static/esp-web-tools/port-3AmrEwYL.js", SHA256: "c004140848b24c16c4d12102346f090b7ab3a8262fe073e695d7bff2e8337d3b", Notice: true},
			{Path: "internal/web/static/esp-web-tools/styles-BiOOVrm2.js", SHA256: "e74c11bc88ea2a0011352b1442b3217c8035f456c5e0463c31b77e329e1e6a1d", Notice: true},
		},
		Modified: "Prepended an attribution banner to every file (the minified build ships none, and its bundled MIT and BSD code needs one). Bodies are byte-identical to upstream dist/web/.",
		Note:     "Installs observer firmware on ESP32 boards over Web Serial (or WebUSB on Android), on the observer install page. It is the browser build: install-button.js defines the <esp-web-install-button> element and loads the other files as ES modules by relative path, so they live together in their own static directory and keep their upstream names. The bundled third-party code is listed in the entries that follow, with versions from the package-lock.json at the 10.5.2 tag. The USB-serial drivers (cdc-acm, ch34x, cp210x, ftdi) are esp-web-tools' own code.",
	},
	{
		Name:        "Lit",
		Version:     "3.3.3",
		SPDX:        "BSD-3-Clause",
		Homepage:    "https://lit.dev",
		Kind:        KindBundled,
		Source:      "https://registry.npmjs.org/lit/-/lit-3.3.3.tgz",
		LicenseText: "lit-3.3.3.txt",
		Note:        "Bundled inside esp-web-tools (styles-*.js), which renders its install dialog with it. Covers the Lit packages it pulls in, all under this same Google LLC license: lit 3.3.3, lit-html 3.3.0, lit-element 4.2.0, @lit/reactive-element 2.1.0 and @lit/context 1.1.6.",
	},
	{
		Name:        "Material Web",
		Version:     "2.5.0",
		SPDX:        "Apache-2.0",
		Homepage:    "https://github.com/material-components/material-web",
		Kind:        KindBundled,
		Source:      "https://registry.npmjs.org/@material/web/-/web-2.5.0.tgz",
		LicenseText: "material-web-2.5.0.txt",
		Note:        "Bundled inside esp-web-tools: the dialog, buttons and list components of its install dialog (@material/web).",
	},
	{
		Name:        "Improv Wi-Fi serial SDK",
		Version:     "3.0.0",
		SPDX:        "Apache-2.0",
		Homepage:    "https://github.com/improv-wifi/sdk-serial-js",
		Kind:        KindBundled,
		Source:      "https://registry.npmjs.org/@improv-wifi/serial-sdk/-/serial-sdk-3.0.0.tgz",
		LicenseText: "improv-wifi-serial-sdk-3.0.0.txt",
		Note:        "Bundled inside esp-web-tools (@improv-wifi/serial-sdk). Our manifests set new_install_improv_wait_time to 0 because the observer firmware doesn't speak Improv, so it never runs, but it still ships.",
	},
	{
		Name:        "esptool-js",
		Version:     "0.7.0",
		SPDX:        "Apache-2.0",
		Homepage:    "https://github.com/espressif/esptool-js",
		Kind:        KindBundled,
		Source:      "https://registry.npmjs.org/esptool-js/-/esptool-js-0.7.0.tgz",
		LicenseText: "esptool-js-0.7.0.txt",
		Note:        "Bundled inside esp-web-tools (install-dialog-*.js): the ESP ROM bootloader protocol that writes the firmware.",
	},
	{
		Name:        "esp-flasher-stub",
		Version:     "1.2.2",
		SPDX:        "MIT",
		Homepage:    "https://github.com/espressif/esp-flasher-stub",
		Kind:        KindBundled,
		Source:      "https://github.com/espressif/esptool-js/tree/v0.7.0/src/targets/stub_flasher",
		LicenseText: "esp-flasher-stub-1.2.2.txt",
		Note:        "The flasher stub binaries esptool-js uploads to the chip's RAM to speed up flashing, one per chip (the esp32*.js and esp8266-*.js files). esptool-js copied them from esp-flasher-stub v1.2.2, where they are dual-licensed MIT OR Apache-2.0; we take them under MIT. These are not the GPL-licensed stubs of the Python esptool.",
	},
	{
		Name:        "pako",
		Version:     "2.2.0",
		SPDX:        "MIT",
		Homepage:    "https://github.com/nodeca/pako",
		Kind:        KindBundled,
		Source:      "https://registry.npmjs.org/pako/-/pako-2.2.0.tgz",
		LicenseText: "pako-2.2.0.txt",
		Note:        "Bundled inside esp-web-tools via esptool-js, which deflates firmware before sending it to the chip. pako is licensed (MIT AND Zlib): its own code is MIT (this entry), and its port of zlib is under the zlib license (the next entry).",
	},
	{
		Name:        "pako zlib port",
		Version:     "2.2.0",
		SPDX:        "Zlib",
		Homepage:    "https://github.com/nodeca/pako",
		Kind:        KindBundled,
		Source:      "https://registry.npmjs.org/pako/-/pako-2.2.0.tgz (lib/zlib/)",
		LicenseText: "pako-zlib-2.2.0.txt",
		Note:        "The zlib-derived part of pako (lib/zlib/), under the zlib license. The upstream package has no separate license file for it, so the text here is the notice at the top of each of those source files.",
	},
	{
		Name:        "atob-lite",
		Version:     "2.0.0",
		SPDX:        "MIT",
		Homepage:    "https://github.com/hughsk/atob-lite",
		Kind:        KindBundled,
		Source:      "https://registry.npmjs.org/atob-lite/-/atob-lite-2.0.0.tgz",
		LicenseText: "atob-lite-2.0.0.txt",
		Note:        "Bundled inside esp-web-tools via esptool-js (its browser build, a one-line wrapper around atob). Author Hugh Kennedy; the license text names no copyright holder.",
	},
	{
		Name:        "tslib",
		Version:     "2.9.1",
		SPDX:        "0BSD",
		Homepage:    "https://github.com/microsoft/tslib",
		Kind:        KindBundled,
		Source:      "https://registry.npmjs.org/tslib/-/tslib-2.9.1.tgz",
		LicenseText: "tslib-2.9.1.txt",
		Note:        "TypeScript's runtime helpers, bundled inside esp-web-tools.",
	},
	{
		Name:        "Tabler",
		Version:     "1.7.1",
		SPDX:        "MIT",
		Homepage:    "https://tabler.io",
		Kind:        KindAsset,
		Source:      "https://registry.npmjs.org/@tabler/core/-/core-1.7.1.tgz (dist/css/tabler.min.css, dist/css/tabler-themes.min.css, dist/js/tabler.min.js)",
		LicenseText: "tabler-1.7.1.txt",
		Files: []File{
			{Path: "internal/web/static/tabler.min.js", SHA256: "0c804e9d4d33f0c3847fcc1a4d593563894237682fd1d7eeea66a48a4a4a8ad3", Notice: true},
			{Path: "internal/web/static/tabler.min.css", SHA256: "413912dcb8c951ba1ec7272b6611259a1fdb225425738da2b83a20d93855fd33", Notice: true},
			{Path: "internal/web/static/tabler-themes.min.css", SHA256: "fb2852041d45aceb0fd62825db812722b1616f701a749bf4f28ea096a0416f72", Notice: true},
		},
		Modified: "All three have their trailing sourceMappingURL comment stripped, since the .map files are not vendored. tabler.min.js also gets a hand-restored banner, after Tabler's own, for the Bootstrap and Popper code it bundles: Tabler 1.5+ builds Bootstrap from its own tree and its bundle carries neither project's notice. Bodies are byte-identical to upstream (upstream sha256: tabler.min.css dcf84d7b4319c29ab33450bfe2a7bbf607458d994ef71023f7509e912cafdbd5, tabler-themes.min.css 7f863d1f6918e6d9d9f0e8a540c0d45efa567f88caa711a31d2d2253837a544f, tabler.min.js 2a3b9ecae44d821b21ba4521da33fca1bdc692454745536418c4e078e8ba6abf).",
		Note:     "Tabler's paid add-ons (Illustrations, Emails, Avatars) are a Personal License that forbids open-source redistribution — nothing from them may enter this repository. The license text here is from the repository's @tabler/core@1.7.1 tag; the npm package ships no LICENSE file.",
	},
	{
		Name:        "Bootstrap",
		Version:     "5.3.8",
		SPDX:        "MIT",
		Homepage:    "https://getbootstrap.com",
		Kind:        KindBundled,
		Source:      "bundled inside @tabler/core@1.7.1 dist/js/tabler.min.js (its components report VERSION 5.3.8)",
		LicenseText: "bootstrap-5.3.8.txt",
		Note:        "Not vendored directly: Tabler's bundle embeds Bootstrap's JavaScript. It ships to every user, so it is attributed here and in the banner restored at the top of tabler.min.js.",
	},
	{
		Name:        "Popper",
		Version:     "2.11.8",
		SPDX:        "MIT",
		Homepage:    "https://popper.js.org",
		Kind:        KindBundled,
		Source:      "bundled inside @tabler/core@1.7.1 dist/js/tabler.min.js (@tabler/core depends on @popperjs/core ^2.11.8, the last 2.x release); license text from the @popperjs/core@2.11.8 npm package",
		LicenseText: "popper-2.11.8.txt",
		Note:        "Not vendored directly: Bootstrap's dropdowns and tooltips position with Popper, which Tabler's bundle embeds. It ships to every user, so it is attributed here and in the banner restored at the top of tabler.min.js.",
	},
	{
		Name:        "Tabler Icons",
		SPDX:        "MIT",
		Homepage:    "https://tabler.io/icons",
		Kind:        KindArtwork,
		Source:      "https://github.com/tabler/tabler-icons (icon path data, various versions)",
		LicenseText: "tabler-icons.txt",
		Files: []File{
			{Path: "internal/web/templates/icons.html", SHA256: "f4a5e793b675d3173e3a9da8d7c3f74091778bc9551aab79312bd2fb82ed20ee", Notice: true},
		},
		Modified: "Icon path data copied into Go template definitions rather than vendored as SVG files; the transparent 24x24 guard path upstream emits is dropped.",
		Note:     "All 55 icons in icons.html are Tabler Icons; several are renamed locally (antenna<-antenna-bars-5, copy<-squares, list<-list-details, plug<-plug-connected, terminal<-terminal-2, alert<-alert-triangle, brand-signal<-message-circle-2). Version is unpinned because the set was collected across releases.",
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
	"air-console.js",
	"app.css",
	"basemap.js",
	"configure-panes.js",
	"configure.js",
	"console-complete.js",
	"console-config.js",
	"console.js",
	"link-editor.js",
	"listfilter.js",
	"mesh-bridge.js",
	"net-air-form.js",
	"livereload.js",
	"meshmap.js",
	"netcontrol.js",
	"observer-install.js",
	"radio-preset.js",
	"regionmap.js",
	"serial-cli.js",
	"serial-port.js",
	"serial-setup.js",
	"timezone-picker.js",
	"usb-console.js",
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
	// Zlib is permissive and carries no copyleft: keep the notice, don't
	// misrepresent who wrote it, and mark altered source. It's here for pako's
	// zlib port, which esptool-js (inside esp-web-tools) uses to compress
	// firmware for flashing; every browser ESP flasher is built on esptool-js.
	// Approved 2026-11-22.
	"Zlib": true,
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
