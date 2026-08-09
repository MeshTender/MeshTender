package licenses

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The published image is meant to be reproducible: an end user auditing the
// source should be able to rebuild from a clean checkout of a tagged commit
// and get the digest we published. That only holds while every build input
// stays pinned and the pins agree with each other, and those pins live in four
// separate files (mise, go.mod, .ko.yaml, the CI workflow) that nothing else
// forces to move together. These tests are what stops one of them drifting.
//
// They live in this package because it already owns "what we ship and under
// what terms" — the base image is a manifest entry here, and reproducibility is
// the same auditability story as the licensing gate. They read only committed
// files, so they need no network and no module cache.
//
// Deliberately regex rather than a YAML/TOML parser: gopkg.in/yaml.v3 is only
// an indirect dependency today, and promoting it to a direct one to read four
// scalars would add an entry to THIRD-PARTY-NOTICES.md for no real gain.

// ciWorkflow is the pipeline: checks plus the image publish. Deployment lives in a
// separate infrastructure repository, so what this repo can still assert is that
// the build stays pinned and that the digest it publishes is captured and exported
// for whoever deploys it.
const ciWorkflow = ".github/workflows/ci.yml"

// workflowFiles returns every workflow keyed by file name, failing if there are
// none — an empty map would make the loops below pass vacuously.
func workflowFiles(t *testing.T) map[string]string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read .github/workflows: %v", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml")) {
			continue
		}
		out[e.Name()] = readRepoFile(t, filepath.Join(".github", "workflows", e.Name()))
	}
	if len(out) == 0 {
		t.Fatal("no workflows in .github/workflows — has the CI layout changed?")
	}
	return out
}

// digestRE matches the "@sha256:<64 hex>" suffix of a pinned image reference.
var digestRE = regexp.MustCompile(`@sha256:([0-9a-f]{64})\b`)

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// findSubmatch pulls the first capture group of pattern out of s, failing the
// test with a readable message when the line it expects has moved or changed
// shape — a silent "" would make the comparison tests pass vacuously.
func findSubmatch(t *testing.T, s, rel, pattern string) string {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("%s: found no match for %q — did the file's layout change?", rel, pattern)
	}
	return m[1]
}

// TestBaseImageIsPinnedByDigest guards the pin that is easiest to lose: it is
// tempting to write the readable ":nonroot" tag, but that tag moves whenever
// distroless rebuilds, which silently makes every later build unreproducible
// and swaps the base out from under the licensing audit.
func TestBaseImageIsPinnedByDigest(t *testing.T) {
	ko := readRepoFile(t, ".ko.yaml")

	base := findSubmatch(t, ko, ".ko.yaml", `(?m)^defaultBaseImage:\s*(\S+)\s*$`)
	m := digestRE.FindStringSubmatch(base)
	if m == nil {
		t.Fatalf(".ko.yaml: defaultBaseImage %q is not pinned by digest; a floating tag makes the build unreproducible", base)
	}

	// The same image is declared in the licensing manifest, which is what
	// THIRD-PARTY-NOTICES.md is generated from. If the two disagree, the
	// notices describe a base image we do not actually ship.
	var source string
	for _, d := range Deps {
		if d.Kind == KindImage {
			source = d.Source
			break
		}
	}
	if source == "" {
		t.Fatal("no KindImage entry in Deps — the base image must stay declared in the manifest")
	}
	dm := digestRE.FindStringSubmatch(source)
	if dm == nil {
		t.Fatalf("manifest base image Source %q records no digest", source)
	}
	if dm[1] != m[1] {
		t.Errorf("base image digest differs:\n  .ko.yaml:  %s\n  manifest:  %s", m[1], dm[1])
	}
}

// TestReleasePinsAreConsistent checks that the Go toolchain and ko versions
// agree everywhere they are written down. The compiler version changes the
// binary, so a builder image that has drifted from the local pin means CI and a
// developer produce different digests from the same commit.
func TestReleasePinsAreConsistent(t *testing.T) {
	mise := readRepoFile(t, ".config/mise/config.toml")

	goPin := findSubmatch(t, mise, ".config/mise/config.toml", `(?m)^go\s*=\s*"([^"]+)"`)
	if strings.Contains(goPin, "latest") {
		t.Fatalf("mise pins go = %q; it must be an exact version for reproducible builds", goPin)
	}

	gomod := readRepoFile(t, "go.mod")
	goDirective := findSubmatch(t, gomod, "go.mod", `(?m)^go (\S+)\s*$`)
	if goDirective != goPin {
		t.Errorf("Go version differs: go.mod go %s, mise go = %q", goDirective, goPin)
	}

	// GOTOOLCHAIN is what actually forces the exact compiler; the go.mod `go`
	// line alone is only a minimum, so a developer on a newer Go would
	// otherwise build a different binary from the same commit.
	miseToolchain := findSubmatch(t, mise, ".config/mise/config.toml", `(?m)^GOTOOLCHAIN\s*=\s*"go([^"]+)"`)
	if miseToolchain != goPin {
		t.Errorf("mise GOTOOLCHAIN is go%s, want go%s", miseToolchain, goPin)
	}

	// Every workflow step that compiles Go must use the pinned toolchain. They do
	// it by reading go.mod (checked above against the mise pin) rather than naming a
	// version, so a literal here would be a second pin to forget — assert there
	// isn't one.
	for name, body := range workflowFiles(t) {
		for _, m := range regexp.MustCompile(`(?m)^\s*go-version:\s*(\S+)`).FindAllStringSubmatch(body, -1) {
			t.Errorf(".github/workflows/%s: pins go-version: %s. Use go-version-file: go.mod so the "+
				"toolchain has exactly one source of truth.", name, m[1])
		}
		if !strings.Contains(body, "go-version-file: go.mod") {
			t.Errorf(".github/workflows/%s: sets up Go without go-version-file: go.mod", name)
		}
		// GOTOOLCHAIN=local forbids fetching a different toolchain; go<pin> names the
		// same one explicitly. Anything else (notably "auto") lets a build silently
		// use a compiler we did not pin, which changes the binary.
		tc := findSubmatch(t, body, ".github/workflows/"+name, `(?m)^\s*GOTOOLCHAIN:\s*(\S+)`)
		if tc != "local" && tc != "go"+goPin {
			t.Errorf(".github/workflows/%s: GOTOOLCHAIN is %q, want \"local\" or %q", name, tc, "go"+goPin)
		}
	}

	// ko itself is a build input: a different ko can lay out layers
	// differently, so its version is pinned in both places too.
	koPin := findSubmatch(t, mise, ".config/mise/config.toml", `(?m)^"aqua:ko-build/ko"\s*=\s*"([^"]+)"`)
	ci := readRepoFile(t, ciWorkflow)
	koCI := findSubmatch(t, ci, ciWorkflow, `(?m)^\s*KO_VERSION:\s*v?(\S+)\s*$`)
	if koCI != koPin {
		t.Errorf("ko version differs: mise %q, %s %q", koPin, ciWorkflow, koCI)
	}
}

// nonGatingJobs are the CI jobs that are deliberately not prerequisites of the
// publish job: publish is the thing being gated, and e2e is non-gating on purpose
// (it skips when no browser is reachable, so requiring it would make releases
// depend on an advisory check).
var nonGatingJobs = map[string]bool{
	"publish": true,
	"e2e":     true,
}

// TestBuildDependsOnEveryGatingJob catches a whole class of quiet failure: a check
// that runs, goes red, and is ignored because nothing depends on it. The licensing
// audit sat in exactly that state once — failing on every push while images kept
// building, because the publish gate listed only test, lint and vuln. Adding a
// check is easy to do without wiring it in, and the symptom is invisible (a red
// check beside a green publish), so assert the wiring instead of trusting it.
func TestBuildDependsOnEveryGatingJob(t *testing.T) {
	ci := readRepoFile(t, ciWorkflow)

	// Job names are the two-space-indented keys inside the `jobs:` block. Scoping to
	// that block matters: `on:` has two-space keys of its own ("push"), and counting
	// those as jobs would demand the publish gate depend on a trigger.
	jobsBlock := ci[strings.Index(ci, "\njobs:\n"):]
	if end := regexp.MustCompile(`(?m)^[a-zA-Z#]`).FindStringIndex(jobsBlock[len("\njobs:\n"):]); end != nil {
		jobsBlock = jobsBlock[:len("\njobs:\n")+end[0]]
	}
	jobRE := regexp.MustCompile(`(?m)^  ([a-z0-9][a-z0-9-]*):\s*$`)
	var jobs []string
	for _, m := range jobRE.FindAllStringSubmatch(jobsBlock, -1) {
		jobs = append(jobs, m[1])
	}
	if len(jobs) == 0 {
		t.Fatalf("%s: found no jobs — has the CI layout changed?", ciWorkflow)
	}

	needsLine := findSubmatch(t, ci, ciWorkflow, `(?m)^\s*needs:\s*\[([^\]]*)\]`)
	declared := map[string]bool{}
	for _, n := range strings.Split(needsLine, ",") {
		if n = strings.TrimSpace(n); n != "" {
			declared[n] = true
		}
	}

	gating := 0
	for _, name := range jobs {
		if nonGatingJobs[name] {
			continue
		}
		gating++
		if !declared[name] {
			t.Errorf("%s: job %q is a check but publish does not list it in needs, so it can fail "+
				"while the image still publishes. Add it to needs, or to nonGatingJobs with a reason.",
				ciWorkflow, name)
		}
	}
	if gating == 0 {
		t.Errorf("%s: found no gating jobs — has the CI layout changed?", ciWorkflow)
	}

	// A name in needs that matches no job makes the whole workflow invalid, but the
	// error only shows up when CI next runs.
	for name := range declared {
		if !slices.Contains(jobs, name) {
			t.Errorf("%s: publish needs %q, which is not a job in this workflow", ciWorkflow, name)
		}
	}
}

// TestPublishExportsTheImageDigest guards the plumbing behind /version's imageDigest
// field, which is the value an auditor compares their own rebuild against.
//
// It is a chain with no runtime signal when it breaks: ko has to be asked for the
// published reference (--image-refs), and this workflow has to export the digest for
// the deployer to pass back in as MESHTENDER_IMAGE_DIGEST. Deployment lives in
// another repository now, so this repo can only assert its own half — but that half
// is the one that silently stops producing a value, after which /version quietly
// answers with less than it claims to.
func TestPublishExportsTheImageDigest(t *testing.T) {
	ci := stripYAMLComments(readRepoFile(t, ciWorkflow))

	if !strings.Contains(ci, "--image-refs") {
		t.Errorf("%s: ko is not run with --image-refs, so the published digest is never captured "+
			"and there is nothing to hand the deployer", ciWorkflow)
	}
	// The digest has to leave the job: an output for a caller, and the run summary
	// for a human doing it by hand.
	if !regexp.MustCompile(`digest=\$\{ref#\*@\}|digest=.*GITHUB_OUTPUT`).MatchString(ci) &&
		!strings.Contains(ci, `digest: ${{ steps.build.outputs.digest }}`) {
		t.Errorf("%s: the publish job does not export the digest it pushed", ciWorkflow)
	}
	if !strings.Contains(ci, "MESHTENDER_IMAGE_DIGEST") {
		t.Errorf("%s: nothing names MESHTENDER_IMAGE_DIGEST, so the contract with the deployer "+
			"(pass this digest back so /version can report it) is undocumented where it is produced",
			ciWorkflow)
	}
}

// TestImageDigestEnvVarMatchesConfig keeps the deploy and the app agreeing on the
// variable's name. They are two files with no compiler between them, and a
// mismatch is invisible: the app just never sees a digest.
func TestImageDigestEnvVarMatchesConfig(t *testing.T) {
	cfg := readRepoFile(t, "internal/config/config.go")
	name := findSubmatch(t, cfg, "internal/config/config.go", `os\.Getenv\("(MESHTENDER_IMAGE_DIGEST)"\)`)

	ci := stripYAMLComments(readRepoFile(t, ciWorkflow))
	if !strings.Contains(ci, name) {
		t.Errorf("%s does not mention %s, the variable internal/config reads and the deployer "+
			"has to set", ciWorkflow, name)
	}
}

// stripYAMLComments drops whole-line # comments. Crude on purpose — it is used only
// to keep prose in CI files out of checks that look for code, so that a check can't
// pass on a comment describing the thing it is looking for.
func stripYAMLComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// TestLintVersionsAgree pins a three-way relationship that has no local signal at
// all: .golangci.yml declares a config-format major, golangci-lint's major has to
// match it, and golangci-lint-action's major has to be one that drives that
// golangci-lint. Getting it wrong fails only on the runner, with a message about
// version strings rather than about the mismatch — which is how it failed once
// already (action v6 pinned against golangci-lint v2).
//
// The action-to-lint mapping is the part worth writing down: action v6 drives
// golangci-lint v1, and v7 onward drive v2.
func TestLintVersionsAgree(t *testing.T) {
	cfgMajor := findSubmatch(t, readRepoFile(t, ".golangci.yml"), ".golangci.yml", `(?m)^version:\s*"?(\d+)"?\s*$`)

	ci := readRepoFile(t, ciWorkflow)
	actionMajor := findSubmatch(t, ci, ciWorkflow, `golangci/golangci-lint-action@v(\d+)`)
	lintMajor := findSubmatch(t, ci, ciWorkflow, `(?m)^\s*version:\s*v(\d+)\.\d+`)

	if lintMajor != cfgMajor {
		t.Errorf("%s pins golangci-lint v%s.x but .golangci.yml is config version %s; the linter "+
			"cannot read a config from a different major", ciWorkflow, lintMajor, cfgMajor)
	}

	// Minimum action major that understands each golangci-lint major.
	minAction := map[string]int{"1": 6, "2": 7}
	want, known := minAction[lintMajor]
	if !known {
		t.Fatalf("golangci-lint v%s.x is newer than this test knows about — check which "+
			"golangci-lint-action majors support it and extend minAction", lintMajor)
	}
	got, err := strconv.Atoi(actionMajor)
	if err != nil {
		t.Fatalf("%s: unparseable golangci-lint-action major %q", ciWorkflow, actionMajor)
	}
	if got < want {
		t.Errorf("%s uses golangci-lint-action@v%d with golangci-lint v%s.x, which it cannot drive; "+
			"v%d or newer is required", ciWorkflow, got, lintMajor, want)
	}
}
