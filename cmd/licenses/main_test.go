package main

import (
	"sort"
	"testing"
)

// TestScanModulesIsHostIndependent is a regression test.
//
// `go list -deps` answers for exactly one GOOS/GOARCH, and the module set
// genuinely differs between them: Linux pulls in moby/sys/userns and
// tklauser/numcpus, macOS pulls in ebitengine/purego. scanModules used to
// inherit the host's platform, so THIRD-PARTY-NOTICES.md generated on a macOS
// laptop listed 87 modules while the Linux CI runner computed 88 — the drift
// check failed on every CI run and regenerating locally could not fix it.
//
// scanModules now pins GOOS/GOARCH per invocation and unions a fixed matrix, so
// the host must not matter. Setting GOOS/GOARCH in the environment is what the
// old code was (wrongly) sensitive to, which is precisely what this asserts is
// no longer true.
//
// This shells out to `go list`, so it needs a module cache — the same
// requirement `go test ./...` already has.
func TestScanModulesIsHostIndependent(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}

	scanAs := func(goos, goarch string) []string {
		t.Helper()
		t.Setenv("GOOS", goos)
		t.Setenv("GOARCH", goarch)

		mods, _, err := scanModules(root)
		if err != nil {
			t.Fatalf("scanModules as %s/%s: %v", goos, goarch, err)
		}
		paths := make([]string, 0, len(mods))
		for _, m := range mods {
			paths = append(paths, m.Path)
		}
		sort.Strings(paths)
		return paths
	}

	asLinux := scanAs("linux", "amd64")
	asDarwin := scanAs("darwin", "arm64")

	if len(asLinux) == 0 {
		t.Fatal("scanned no modules at all — is the module cache populated?")
	}
	if len(asLinux) != len(asDarwin) {
		t.Fatalf("module count depends on the host platform: linux/amd64 saw %d, darwin/arm64 saw %d", len(asLinux), len(asDarwin))
	}
	for i := range asLinux {
		if asLinux[i] != asDarwin[i] {
			t.Errorf("module list depends on the host platform: linux/amd64 has %q where darwin/arm64 has %q", asLinux[i], asDarwin[i])
		}
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
