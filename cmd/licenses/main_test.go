package main

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// probePkg is a single package whose test dependencies differ between Linux and
// macOS (via testcontainers/gopsutil). Listing one package keeps this test cheap
// — scanning ./... across the whole matrix costs ~9 `go list` runs per call, and
// doing that twice dominated the CI test step.
const probePkg = "./internal/store"

func probeArgs() []string {
	return []string{"list", "-deps", "-test", "-json", probePkg}
}

func paths(mods map[string]moduleInfo) []string {
	out := make([]string, 0, len(mods))
	for p := range mods {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// TestListModulesPinsPlatform is a regression test.
//
// `go list -deps` answers for exactly one GOOS/GOARCH, and the module set
// genuinely differs between them: Linux pulls in moby/sys/userns and
// tklauser/numcpus, macOS pulls in ebitengine/purego. listModules used to
// inherit the host's platform, so THIRD-PARTY-NOTICES.md generated on a macOS
// laptop listed 87 modules while the Linux CI runner computed 88 — the drift
// check failed on every CI run and regenerating locally could not fix it.
//
// Two things have to hold, and checking only one of them is how this would rot:
// the requested platform must actually take effect, and the host's own
// GOOS/GOARCH must not leak through.
//
// This shells out to `go list`, so it needs a module cache — the same
// requirement `go test ./...` already has.
func TestListModulesPinsPlatform(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}

	list := func(p platform) []string {
		t.Helper()
		mods, err := listModules(root, p, probeArgs())
		if err != nil {
			t.Fatalf("listModules for %s/%s: %v", p.GOOS, p.GOARCH, err)
		}
		if len(mods) == 0 {
			t.Fatalf("listModules for %s/%s found nothing — is the module cache populated?", p.GOOS, p.GOARCH)
		}
		return paths(mods)
	}

	linux := list(platform{GOOS: "linux", GOARCH: "amd64"})
	darwin := list(platform{GOOS: "darwin", GOARCH: "arm64"})

	// Positive control. Two things make these match: listModules ignoring the
	// platform it was handed (the regression this guards — both lists then come
	// from the host), or probePkg's dependencies no longer differing by
	// platform, which would leave the test proving nothing.
	if slices.Equal(linux, darwin) {
		t.Fatalf("%s resolved identically for linux/amd64 and darwin/arm64 (%d modules each). "+
			"Either listModules is not applying the platform it was given, or probePkg no longer "+
			"has platform-specific dependencies and this test needs re-pointing.", probePkg, len(linux))
	}

	// The actual regression: the host must not influence the answer.
	t.Setenv("GOOS", "darwin")
	t.Setenv("GOARCH", "arm64")
	if got := list(platform{GOOS: "linux", GOARCH: "amd64"}); !slices.Equal(got, linux) {
		t.Errorf("a linux/amd64 listing changed when the host env said darwin/arm64: %d modules vs %d", len(got), len(linux))
	}
}

// TestAuditPlatformsCoversShipAndDev guards the matrix itself: dropping it back
// to a single platform would silently reintroduce the host dependency above,
// and dropping the ship platform would mean the notices file describes
// something we do not publish.
func TestAuditPlatformsCoversShipAndDev(t *testing.T) {
	var haveShip bool
	oses := map[string]bool{}
	for _, p := range auditPlatforms {
		oses[p.GOOS] = true
		if p == shipPlatform {
			haveShip = true
		}
	}
	if !haveShip {
		t.Errorf("auditPlatforms does not include shipPlatform %s/%s", shipPlatform.GOOS, shipPlatform.GOARCH)
	}
	if len(oses) < 2 {
		t.Errorf("auditPlatforms covers only %v — scanning one GOOS makes the generated file host-dependent", oses)
	}
}

// copyFixture copies testdata/dual (Eclipse Paho's LICENSE, edl-v10 and
// NOTICE.md, verbatim) into a temp module dir, minus any files named in skip,
// with NOTICE.md's declaration replaced when notice is non-empty.
func copyFixture(t *testing.T, notice string, skip ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"LICENSE", "edl-v10", "NOTICE.md"} {
		if slices.Contains(skip, name) {
			continue
		}
		b, err := os.ReadFile(filepath.Join("testdata", "dual", name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "NOTICE.md" && notice != "" {
			b = []byte(strings.ReplaceAll(string(b), "SPDX-License-Identifier: EPL-2.0 or BSD-3-Clause", notice))
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestIdentifyHonorsDeclaredDualLicense: Eclipse Paho's LICENSE file is the
// EPL-2.0 text, but the project is dual-licensed EPL-2.0 OR BSD-3-Clause (its
// NOTICE.md says so in SPDX form) and ships the BSD text as edl-v10. The
// scanner used to read only LICENSE and reject the module as EPL.
func TestIdentifyHonorsDeclaredDualLicense(t *testing.T) {
	t.Parallel()
	spdx, copyrights, err := identify(copyFixture(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if spdx != "BSD-3-Clause" {
		t.Fatalf("spdx = %s, want BSD-3-Clause", spdx)
	}
	if !slices.ContainsFunc(copyrights, func(c string) bool { return strings.Contains(c, "Eclipse Foundation") }) {
		t.Errorf("copyrights = %q; the BSD notice needs edl-v10's copyright line", copyrights)
	}
}

func TestIdentifyDualLicenseNeedsGrantAndText(t *testing.T) {
	t.Parallel()
	for name, dir := range map[string]string{
		// Declared, but the BSD text isn't shipped: nothing to reproduce in a notice.
		"text missing": copyFixture(t, "", "edl-v10"),
		// The BSD text is there, but nothing grants a choice.
		"no declaration": copyFixture(t, "SPDX-License-Identifier: EPL-2.0"),
		// AND obliges both licenses; it isn't a choice.
		"conjunction": copyFixture(t, "SPDX-License-Identifier: EPL-2.0 AND BSD-3-Clause"),
		// A license exception isn't an alternative either.
		"with exception": copyFixture(t, "SPDX-License-Identifier: EPL-2.0 WITH Classpath-exception-2.0"),
	} {
		spdx, _, err := identify(dir)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if spdx != "EPL-2.0" {
			t.Errorf("%s: spdx = %s, want EPL-2.0 (rejected later as not permissive)", name, spdx)
		}
	}
}

func TestDeclaredAlternatives(t *testing.T) {
	t.Parallel()
	for in, want := range map[string][]string{
		"SPDX-License-Identifier: EPL-2.0 or BSD-3-Clause":         {"EPL-2.0", "BSD-3-Clause"},
		"// SPDX-License-Identifier: (MIT OR Apache-2.0)":          {"MIT", "Apache-2.0"},
		"SPDX-License-Identifier: MIT":                             nil,
		"SPDX-License-Identifier: GPL-2.0 AND MIT":                 nil,
		"SPDX-License-Identifier: MIT OR":                          nil,
		"no declaration here":                                      nil,
		"SPDX-License-Identifier: A OR B AND C":                    nil,
		"SPDX-License-Identifier: GPL-2.0 WITH Linux-syscall-note": nil,
	} {
		if got := declaredAlternatives(in); !slices.Equal(got, want) {
			t.Errorf("declaredAlternatives(%q) = %q, want %q", in, got, want)
		}
	}
}
