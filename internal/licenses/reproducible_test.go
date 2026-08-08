package licenses

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The published image is meant to be reproducible: an end user auditing the
// source should be able to rebuild from a clean checkout of a tagged commit
// and get the digest we published. That only holds while every build input
// stays pinned and the pins agree with each other, and those pins live in four
// separate files that nothing else forces to move together. These tests are
// what stops one of them drifting.
//
// They live in this package because it already owns "what we ship and under
// what terms" — the base image is a manifest entry here, and reproducibility is
// the same auditability story as the licensing gate. They read only committed
// files, so they need no network and no module cache.
//
// Deliberately regex rather than a YAML/TOML parser: gopkg.in/yaml.v3 is only
// an indirect dependency today, and promoting it to a direct one to read four
// scalars would add an entry to THIRD-PARTY-NOTICES.md for no real gain.

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

	// Every Woodpecker step that compiles Go must use the same toolchain.
	wantImage := "golang:" + goPin
	entries, err := os.ReadDir(filepath.Join(repoRoot(t), ".woodpecker"))
	if err != nil {
		t.Fatalf("read .woodpecker: %v", err)
	}
	golangImageRE := regexp.MustCompile(`golang:[0-9][^\s"']*`)
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		body := readRepoFile(t, filepath.Join(".woodpecker", e.Name()))
		for _, got := range golangImageRE.FindAllString(body, -1) {
			checked++
			if got != wantImage {
				t.Errorf(".woodpecker/%s: uses %s, want %s", e.Name(), got, wantImage)
			}
		}
	}
	if checked == 0 {
		t.Error("found no golang:<version> builder image in .woodpecker — has the CI layout changed?")
	}

	// ko itself is a build input: a different ko can lay out layers
	// differently, so its version is pinned in both places too.
	koPin := findSubmatch(t, mise, ".config/mise/config.toml", `(?m)^"aqua:ko-build/ko"\s*=\s*"([^"]+)"`)
	build := readRepoFile(t, ".woodpecker/build.yaml")
	koCI := findSubmatch(t, build, ".woodpecker/build.yaml", `(?m)^\s*KO_VERSION:\s*v?(\S+)\s*$`)
	if koCI != koPin {
		t.Errorf("ko version differs: mise %q, .woodpecker/build.yaml %q", koPin, koCI)
	}
}

// nonGatingWorkflows are the .woodpecker workflows that are deliberately not
// prerequisites of the build: build is the thing being gated (and now carries
// the deploy step too), and e2e is non-gating on purpose (it skips when no
// browser container is up, so requiring it would make releases depend on an
// advisory check).
var nonGatingWorkflows = map[string]bool{
	"build": true,
	"e2e":   true,
}

// TestBuildDependsOnEveryGatingWorkflow catches a whole class of quiet failure:
// a check that runs, goes red, and is ignored because nothing depends on it.
// The licensing audit sat in exactly that state — failing on every push while
// images kept building and deploying, because build.yaml listed only test, lint
// and vuln. Adding a new check is easy to do without wiring it in, and the
// symptom is invisible (a red workflow next to a green deploy), so assert the
// wiring instead of trusting it.
func TestBuildDependsOnEveryGatingWorkflow(t *testing.T) {
	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, ".woodpecker"))
	if err != nil {
		t.Fatalf("read .woodpecker: %v", err)
	}

	build := readRepoFile(t, ".woodpecker/build.yaml")
	// The depends_on block runs to the next top-level key.
	dependsRE := regexp.MustCompile(`(?ms)^depends_on:\s*\n((?:\s*-\s*\S+\s*\n)+)`)
	m := dependsRE.FindStringSubmatch(build)
	if m == nil {
		t.Fatal(".woodpecker/build.yaml: found no depends_on list")
	}
	declared := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(m[1]), "\n") {
		declared[strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))] = true
	}

	found := 0
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".yaml")
		if e.IsDir() || !ok || nonGatingWorkflows[name] {
			continue
		}
		found++
		if !declared[name] {
			t.Errorf(".woodpecker/%s.yaml is a check but build.yaml does not depend on it, "+
				"so it can fail while the image still builds and deploys. Add it to depends_on, "+
				"or to nonGatingWorkflows with a reason.", name)
		}
	}
	if found == 0 {
		t.Error("found no gating workflows in .woodpecker — has the CI layout changed?")
	}

	// A name in depends_on that matches no workflow file is silently ignored by
	// Woodpecker, which looks like a gate but is not one.
	for name := range declared {
		if _, err := os.Stat(filepath.Join(root, ".woodpecker", name+".yaml")); err != nil {
			t.Errorf("build.yaml depends on %q, which is not a .woodpecker workflow", name)
		}
	}
}

// TestDeployReportsTheImageDigest guards the plumbing behind /version's
// imageDigest field, which is the value an auditor compares their own rebuild
// against.
//
// It is a chain with no runtime signal when it breaks: ko has to be asked for
// the published reference (--image-refs), the deploy has to read that file, and
// it has to pass the digest to the app as MESHTENDER_IMAGE_DIGEST. Drop any link
// and nothing fails — the field simply stops appearing, and the endpoint quietly
// answers with less than it claims to. So assert the wiring rather than trust it.
func TestDeployReportsTheImageDigest(t *testing.T) {
	// Every check below reads the COMMENT-STRIPPED file. The step documents this
	// plumbing at length — including quoting the ${...} form that must not appear
	// — so matching against the prose would let a check pass on its own
	// explanation while the command it describes was gone.
	build := stripYAMLComments(readRepoFile(t, ".woodpecker/build.yaml"))

	if !strings.Contains(build, "--image-refs") {
		t.Error(".woodpecker/build.yaml: ko is not run with --image-refs, so the published " +
			"digest is never captured and the deploy has nothing to report")
	}
	if !strings.Contains(build, "MESHTENDER_IMAGE_DIGEST") {
		t.Error(".woodpecker/build.yaml: the deploy does not set MESHTENDER_IMAGE_DIGEST, " +
			"so /version cannot report the digest it is running")
	}
	// Deploying by tag would leave the Deployment naming a mutable reference,
	// and the reported digest could then describe a different artifact than the
	// one that actually got pulled. The patch is a JSON string inside YAML, so
	// its quotes are backslash-escaped.
	if !regexp.MustCompile(`\\?"image\\?":\s*\\?"\$IMAGE\\?"`).MatchString(build) {
		t.Error(".woodpecker/build.yaml: the deploy no longer sets the image from the " +
			"digest-bearing reference ko reported")
	}

	// Woodpecker substitutes ${VAR} itself, before the shell sees it, so a shell
	// variable written that way silently becomes empty. That would deploy an
	// image with an empty digest env var — which the app rejects at startup.
	if regexp.MustCompile(`\$\{(IMAGE|DIGEST)\b`).MatchString(build) {
		t.Error(".woodpecker/build.yaml: a shell variable is written as ${...}, which " +
			"Woodpecker expands away before bash runs. Use $VAR or $(...) instead.")
	}
}

// stripYAMLComments drops whole-line # comments. Crude on purpose — it is used
// only to keep prose in .woodpecker files out of checks that look for code.
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

// TestImageDigestEnvVarMatchesConfig keeps the deploy and the app agreeing on the
// variable's name. They are two files with no compiler between them, and a
// mismatch is invisible: the app just never sees a digest.
func TestImageDigestEnvVarMatchesConfig(t *testing.T) {
	cfg := readRepoFile(t, "internal/config/config.go")
	name := findSubmatch(t, cfg, "internal/config/config.go", `os\.Getenv\("(MESHTENDER_IMAGE_DIGEST)"\)`)

	build := stripYAMLComments(readRepoFile(t, ".woodpecker/build.yaml"))
	if !strings.Contains(build, name) {
		t.Errorf(".woodpecker/build.yaml does not set %s, the variable internal/config reads", name)
	}
}

// TestWoodpeckerVariablesAreParseable catches a failure with an unusually bad
// signal-to-effort ratio: Woodpecker substitutes variables over the RAW pipeline
// file — comments included — before parsing any YAML. A brace-wrapped token that
// isn't a valid variable name kills the whole pipeline with "unable to parse
// variable name", naming neither the file nor the line, and no step ever runs. A
// comment that merely *describes* the syntax is enough to trigger it.
//
// So: every ${…} in .woodpecker must open with something that could actually be
// a variable name. Bash operators after the name (${VAR##glob}, ${VAR:-default})
// are fine — only the name itself is checked.
func TestWoodpeckerVariablesAreParseable(t *testing.T) {
	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, ".woodpecker"))
	if err != nil {
		t.Fatalf("read .woodpecker: %v", err)
	}

	braced := regexp.MustCompile(`\$\{([^}]*)\}`)
	validName := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		rel := filepath.Join(".woodpecker", e.Name())
		body := readRepoFile(t, rel)
		for _, m := range braced.FindAllStringSubmatch(body, -1) {
			checked++
			if !validName.MatchString(m[1]) {
				t.Errorf("%s: %q is not a parseable variable reference. Woodpecker rejects the "+
					"whole pipeline for this — including in comments, so describe the syntax "+
					"rather than quoting it.", rel, m[0])
			}
		}
	}
	if checked == 0 {
		t.Error("found no ${...} references in .woodpecker — has the CI layout changed?")
	}
}
