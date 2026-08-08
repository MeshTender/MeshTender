package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestValidateDigest(t *testing.T) {
	t.Parallel()
	good := "sha256:" + strings.Repeat("a", 64)
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"empty is allowed", "", true},
		{"well formed", good, true},
		{"real looking", "sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35", true},
		{"no algorithm prefix", strings.Repeat("a", 64), false},
		{"wrong algorithm", "sha512:" + strings.Repeat("a", 64), false},
		{"too short", "sha256:" + strings.Repeat("a", 63), false},
		{"too long", "sha256:" + strings.Repeat("a", 65), false},
		{"uppercase hex", "sha256:" + strings.Repeat("A", 64), false},
		{"non hex", "sha256:" + strings.Repeat("g", 64), false},
		{"leading space", " " + good, false},
		{"trailing newline", good + "\n", false},
		{"tagged reference rather than digest", "repo/meshtender:abc123", false},
		{"full reference including digest", "repo/meshtender@" + good, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateDigest(tc.in)
			if tc.ok && err != nil {
				t.Fatalf("ValidateDigest(%q) = %v, want nil", tc.in, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("ValidateDigest(%q) = nil, want an error", tc.in)
			}
		})
	}
}

// TestReadReportsRuntimeFacts checks the fields that come from the toolchain
// rather than from a build stamp, so they hold under `go test` too.
func TestReadReportsRuntimeFacts(t *testing.T) {
	t.Parallel()
	got := Read("")
	if got.Go != runtime.Version() {
		t.Errorf("Go = %q, want %q", got.Go, runtime.Version())
	}
	if got.OS != runtime.GOOS || got.Arch != runtime.GOARCH {
		t.Errorf("OS/Arch = %s/%s, want %s/%s", got.OS, got.Arch, runtime.GOOS, runtime.GOARCH)
	}
	if got.ImageDigest != "" {
		t.Errorf("ImageDigest = %q, want empty when none is supplied", got.ImageDigest)
	}
}

// TestReadHashesTheRunningExecutable is the check that matters for the
// self-attestation claim: the reported hash must be of the file this process is
// actually running from, not of some other path.
func TestReadHashesTheRunningExecutable(t *testing.T) {
	t.Parallel()
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable unavailable on this platform: %v", err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Skipf("cannot read the test binary: %v", err)
	}
	sum := sha256.Sum256(b)
	want := hex.EncodeToString(sum[:])

	if got := Read("").ExecutableSHA256; got != want {
		t.Errorf("ExecutableSHA256 = %q, want %q (the running test binary)", got, want)
	}
}

// TestReadPassesThroughImageDigest documents that Read does not validate — the
// caller does, at startup, so a bad value fails the server rather than being
// silently dropped from an endpoint someone is auditing.
func TestReadPassesThroughImageDigest(t *testing.T) {
	t.Parallel()
	want := "sha256:" + strings.Repeat("b", 64)
	if got := Read(want).ImageDigest; got != want {
		t.Errorf("ImageDigest = %q, want %q", got, want)
	}
}

func TestReproducible(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   Info
		want bool
	}{
		{"clean build of a known commit", Info{Commit: "abc"}, true},
		{"dirty tree", Info{Commit: "abc", Modified: true}, false},
		{"no commit stamp", Info{}, false},
		{"dirty and unstamped", Info{Modified: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.in.Reproducible(); got != tc.want {
				t.Errorf("Reproducible() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestJSONContract pins the wire names. They are the public /version contract,
// so a rename has to be a deliberate edit to this test, not a silent side effect
// of renaming a Go field.
func TestJSONContract(t *testing.T) {
	t.Parallel()
	full := Info{
		Commit: "c0ffee", CommitTime: "2026-08-06T17:47:49-04:00", Modified: true,
		Go: "go1.26.5", OS: "linux", Arch: "amd64",
		ExecutableSHA256: strings.Repeat("a", 64),
		ImageDigest:      "sha256:" + strings.Repeat("b", 64),
	}
	b, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"commit", "commitTime", "modified", "go", "os", "arch", "executableSHA256", "imageDigest"} {
		if _, ok := m[k]; !ok {
			t.Errorf("marshaled Info has no %q key; the /version contract changed", k)
		}
	}

	// A from-source run omits the empty claims rather than reporting "" for
	// them, so a consumer can tell "not stamped" from "stamped as empty".
	b, err = json.Marshal(Info{Go: "go1.26.5", OS: "linux", Arch: "amd64"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, k := range []string{"commit", "commitTime", "executableSHA256", "imageDigest"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("empty %s should be omitted, got %s", k, b)
		}
	}
	// modified is NOT omitempty: false is a meaningful claim (a clean tree),
	// and omitting it would read the same as "we didn't say".
	if !strings.Contains(string(b), `"modified":false`) {
		t.Errorf("modified must always be reported, got %s", b)
	}
}
