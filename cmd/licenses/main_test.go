package main

import (
	"slices"
	"sort"
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
