// Command licenses audits every third-party dependency and keeps
// THIRD-PARTY-NOTICES.md current.
//
// It scans the Go module graph — resolving each module to its license file and
// identifying that file with github.com/google/licensecheck — and combines the
// result with the non-Go manifest in internal/licenses. Anything whose license
// is not on the permissive allowlist fails the run: copyleft terms would reach
// back and constrain how MeshTender itself may be licensed and distributed, so
// they are excluded as a matter of policy rather than preference.
//
// Run it through mise:
//
//	mise run licenses             # check; non-zero exit on a problem or on drift
//	mise run licenses --update    # rewrite THIRD-PARTY-NOTICES.md
//
// The Go-module half of the notices file can only be regenerated where a module
// cache exists, which is why the offline test in internal/licenses checks the
// manifest-derived half and CI runs this command for the rest.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/licensecheck"
	"github.com/jleight/meshtender/internal/licenses"
)

// minCoverage mirrors the threshold the package test uses.
const minCoverage = 90.0

func main() {
	update := flag.Bool("update", false, "rewrite THIRD-PARTY-NOTICES.md instead of only checking it")
	flag.Parse()

	if err := run(*update); err != nil {
		fmt.Fprintf(os.Stderr, "licenses: %v\n", err)
		os.Exit(1)
	}
}

func run(update bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	mods, problems, err := scanModules(root)
	if err != nil {
		return err
	}

	inBinary := 0
	for _, m := range mods {
		if m.InBinary {
			inBinary++
		}
	}
	fmt.Printf("Go modules scanned: %d (%d linked into the binary, %d build/test only)\n",
		len(mods), inBinary, len(mods)-inBinary)
	fmt.Printf("Manifest entries (non-Go): %d\n", len(licenses.Deps))

	for _, d := range licenses.Deps {
		if d.SPDX != "" && !licenses.AllowedSPDX[d.SPDX] {
			problems = append(problems, fmt.Sprintf("%s declares non-permissive %s", d.Label(), d.SPDX))
		}
	}

	byLicense := map[string]int{}
	for _, m := range mods {
		byLicense[m.SPDX]++
	}
	var ids []string
	for id := range byLicense {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fmt.Println("\nGo module licenses:")
	for _, id := range ids {
		fmt.Printf("  %-32s %d\n", id, byLicense[id])
	}

	if len(problems) > 0 {
		fmt.Fprintln(os.Stderr, "\nProblems:")
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "  - %s\n", p)
		}
		return fmt.Errorf("%d dependency problem(s); every dependency must be "+
			"permissively licensed", len(problems))
	}

	doc, err := licenses.Notices(licenses.GoSection(mods))
	if err != nil {
		return err
	}

	path := filepath.Join(root, licenses.NoticesPath)
	if update {
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil { //nolint:gosec // G306: THIRD-PARTY-NOTICES.md is a committed, world-readable document
			return fmt.Errorf("writing %s: %w", licenses.NoticesPath, err)
		}
		fmt.Printf("\nWrote %s\n", licenses.NoticesPath)
		return nil
	}

	existing, err := os.ReadFile(path) //nolint:gosec // G304: path is repoRoot() + a constant filename, not user input
	if err != nil {
		return fmt.Errorf("reading %s: %w (run `mise run licenses --update`)", licenses.NoticesPath, err)
	}
	if string(existing) != doc {
		return fmt.Errorf("%s is out of date — run `mise run licenses --update` and commit the result",
			licenses.NoticesPath)
	}

	fmt.Printf("\n%s is current. All dependencies are permissively licensed.\n", licenses.NoticesPath)
	return nil
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod found above the working directory")
		}
		dir = parent
	}
}

// platform is a GOOS/GOARCH pair the audit covers.
type platform struct{ GOOS, GOARCH string }

// shipPlatform decides which modules count as "linked into the binary". It is
// the platform we publish images for, so the notices file describes what we
// actually redistribute rather than whatever machine ran the command.
var shipPlatform = platform{GOOS: "linux", GOARCH: "amd64"}

// auditPlatforms is the fixed matrix the module scan unions over.
//
// `go list -deps` resolves imports for one GOOS/GOARCH, and the answer differs
// per platform: Linux pulls in moby/sys/userns and tklauser/numcpus, macOS
// pulls in ebitengine/purego. Scanning only the host therefore made the
// generated file depend on who ran the command — a developer on macOS and the
// Linux CI runner produced different module lists, so the drift check could
// never pass on both. Unioning a FIXED matrix makes the output identical
// everywhere, and is the right answer for a licensing gate anyway: a copyleft
// dependency must not be able to hide behind a GOOS constraint.
//
// Windows is deliberately excluded — it is neither a release target nor a
// supported dev platform, and including it would add four Windows-only
// testcontainers dependencies that we never build or ship. Add an entry here if
// that changes.
var auditPlatforms = []platform{
	{GOOS: "linux", GOARCH: "amd64"},
	{GOOS: "linux", GOARCH: "arm64"},
	{GOOS: "darwin", GOARCH: "amd64"},
	{GOOS: "darwin", GOARCH: "arm64"},
}

// scanModules resolves every module in the build to its license. It asks the go
// tool three questions: which modules the shipped binary links, which the tests
// add, and which the browser-tagged e2e suite adds on top of that — so a
// dependency cannot hide behind a build tag. Each question is asked once per
// auditPlatforms entry, so one cannot hide behind a GOOS constraint either.
func scanModules(root string) ([]licenses.GoModule, []string, error) {
	binary, err := listModules(root, shipPlatform, []string{"list", "-deps", "-json", "./cmd/meshtender"})
	if err != nil {
		return nil, nil, fmt.Errorf("listing binary dependencies: %w", err)
	}

	all := map[string]moduleInfo{}
	for _, p := range auditPlatforms {
		for _, args := range [][]string{
			{"list", "-deps", "-test", "-json", "./..."},
			{"list", "-deps", "-test", "-tags", "browser", "-json", "./..."},
		} {
			mods, err := listModules(root, p, args)
			if err != nil {
				return nil, nil, fmt.Errorf("listing dependencies for %s/%s: %w", p.GOOS, p.GOARCH, err)
			}
			for path, info := range mods {
				if _, ok := all[path]; !ok {
					all[path] = info
				}
			}
		}
	}

	var mods []licenses.GoModule
	var problems []string

	paths := make([]string, 0, len(all))
	for path := range all {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		info := all[path]
		spdx, copyrights, err := identify(info.Dir)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			spdx = "UNKNOWN"
		}
		if spdx != "UNKNOWN" && !licenses.AllowedSPDX[spdx] {
			problems = append(problems, fmt.Sprintf("%s %s is %s, which is not permissive", path, info.Version, spdx))
		}
		_, shipped := binary[path]
		mods = append(mods, licenses.GoModule{
			Path:       path,
			Version:    info.Version,
			SPDX:       spdx,
			Copyrights: copyrights,
			InBinary:   shipped,
		})
	}

	return mods, problems, nil
}

type moduleInfo struct {
	Version string
	Dir     string
}

// listModules runs a `go list -json` invocation and collects the modules behind
// the packages it reports, skipping the standard library and this module itself.
func listModules(root string, p platform, args []string) (map[string]moduleInfo, error) {
	cmd := exec.Command("go", args...) //nolint:gosec // G204: args are the literal `go list` invocations in scanModules, never external input
	cmd.Dir = root
	// Pin the target platform so the result does not depend on the host; see
	// auditPlatforms. CGO is off because a cross-GOOS list cannot use the host
	// C toolchain, and the shipped binary is built with CGO_ENABLED=0 anyway.
	cmd.Env = append(os.Environ(), "GOOS="+p.GOOS, "GOARCH="+p.GOARCH, "CGO_ENABLED=0")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	type pkg struct {
		Module *struct {
			Path    string
			Version string
			Dir     string
			Main    bool
		}
	}

	mods := map[string]moduleInfo{}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			break
		}
		if p.Module == nil || p.Module.Main || p.Module.Dir == "" {
			continue
		}
		mods[p.Module.Path] = moduleInfo{Version: p.Module.Version, Dir: p.Module.Dir}
	}
	return mods, nil
}

// identify finds a module's license file and reads its SPDX ID and copyright
// lines out of it.
func identify(dir string) (string, []string, error) {
	var best licensecheck.Coverage
	var bestIDs []string
	var copyrights []string
	found := false

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry is not fatal to the scan
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "vendor", ".git":
				return fs.SkipDir
			}
			return nil
		}
		if !isLicenseFile(d.Name()) {
			return nil
		}
		b, readErr := os.ReadFile(p) //nolint:gosec // G304: p comes from WalkDir over the module cache directory
		if readErr != nil {
			return nil
		}
		found = true
		cov := licensecheck.Scan(b)
		if cov.Percent > best.Percent {
			best = cov
			bestIDs = nil
			for _, m := range cov.Match {
				bestIDs = append(bestIDs, m.ID)
			}
		}
		copyrights = append(copyrights, copyrightLines(string(b))...)
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	if !found {
		return "", nil, errors.New("no license file found in the module")
	}
	if best.Percent < minCoverage || len(bestIDs) == 0 {
		return "", dedupe(copyrights), fmt.Errorf("license not recognized (best coverage %.0f%%)", best.Percent)
	}

	// Prefer a permissive match when a module offers a dual license.
	sort.Strings(bestIDs)
	for _, id := range bestIDs {
		if licenses.AllowedSPDX[id] {
			return id, dedupe(copyrights), nil
		}
	}
	return bestIDs[0], dedupe(copyrights), nil
}

func isLicenseFile(name string) bool {
	u := strings.ToUpper(name)
	for _, suffix := range []string{".MD", ".TXT", ".CODE"} {
		u = strings.TrimSuffix(u, suffix)
	}
	return strings.HasPrefix(u, "LICENSE") || strings.HasPrefix(u, "LICENCE") ||
		strings.HasPrefix(u, "COPYING") || u == "NOTICE"
}

func copyrightLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if !strings.HasPrefix(lower, "copyright") && !strings.HasPrefix(lower, "(c)") {
			continue
		}
		// Skip the boilerplate sentence from MIT/BSD bodies, which is not a notice.
		if strings.Contains(lower, "above copyright notice") || strings.Contains(lower, "shall be included") {
			continue
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
