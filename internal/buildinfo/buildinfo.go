// Package buildinfo reports what this running binary was built from, so anyone
// can rebuild it and check they get the same artifact.
//
// The published image is reproducible (see "Verifying a build" in README.md):
// every build input is pinned, so a clean checkout of a given commit produces a
// byte-identical image. That property is only useful if a third party can find
// out WHICH commit a running server was built from — otherwise they can
// reproduce a build, but not the one in front of them. This package is that
// missing half.
//
// Three kinds of claim, deliberately kept distinct, because they are worth
// different amounts to someone auditing us:
//
//   - Commit/CommitTime/Modified/Go/OS/Arch come from the Go toolchain's own VCS
//     stamps, recorded at compile time. Nothing in our code chooses them.
//   - ExecutableSHA256 is computed here, at runtime, over the file this process
//     is running from. It is the only field that attests to the actual running
//     process rather than to a build that happened elsewhere.
//   - ImageDigest is supplied by the deployment (MESHTENDER_IMAGE_DIGEST). A
//     binary cannot know its own image digest — the digest is computed over the
//     binary, so a binary containing it would have to contain its own hash — so
//     CI resolves it at publish time and hands it to the deployment. It is a
//     claim by our pipeline, not something the server can verify.
//
// Nothing here is secret: the repository is public, and the whole point is that
// an outsider can check our work.
package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"sync"
)

// Info is what a running server reports about its own build. JSON tags are part
// of the public /version contract — renaming one is a breaking change for anyone
// scripting against it.
type Info struct {
	// Commit is the git revision the binary was built from, or "" for a build
	// made outside a repository (a bare `go build` of an exported tree).
	Commit string `json:"commit,omitempty"`
	// CommitTime is that commit's timestamp, RFC 3339, as stamped by the
	// toolchain. Go stamps it into the binary, so it is a build input: it is
	// part of why a given commit reproduces a given digest.
	CommitTime string `json:"commitTime,omitempty"`
	// Modified reports that the working tree had uncommitted changes at build
	// time. Such a build is NOT reproducible from the commit alone — Commit
	// names a tree the binary was not actually built from.
	Modified bool `json:"modified"`
	// Go is the toolchain version (e.g. "go1.26.5"). A different compiler
	// produces a different binary, so a verifier needs it to match.
	Go string `json:"go"`
	// OS and Arch are the build target. `mise run image` defaults to
	// linux/amd64, so a verifier reproducing an arm64 deployment has to pass
	// --platform to match.
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// ExecutableSHA256 is a hash of the file this process is running from,
	// computed at runtime. Empty if the executable could not be read (it was
	// replaced or unlinked under us), which is reported rather than guessed.
	ExecutableSHA256 string `json:"executableSHA256,omitempty"`
	// ImageDigest is the OCI digest of the image this server is running as, as
	// reported by the deployment. Empty when unset — a from-source run, or a
	// deployment that doesn't supply it.
	ImageDigest string `json:"imageDigest,omitempty"`
}

// Reproducible reports whether Info names a build another party could actually
// reproduce: it must identify a commit, and that commit must describe the tree
// the binary was built from.
func (i Info) Reproducible() bool { return i.Commit != "" && !i.Modified }

// digestRE matches an OCI digest: "sha256:" plus exactly 64 lowercase hex.
var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ValidateDigest checks an image digest supplied by the environment. Empty is
// allowed (no digest reported). Anything else must be a well-formed sha256
// digest: a truncated or mistyped value would be published as though it were an
// attestation, and a verifier comparing against it would conclude the running
// server didn't match when the real problem was a typo in a manifest.
func ValidateDigest(s string) error {
	if s == "" || digestRE.MatchString(s) {
		return nil
	}
	return fmt.Errorf("must be a digest of the form sha256:<64 lowercase hex>, got %q", s)
}

// exeHash caches the executable hash. Hashing reads the whole binary (tens of
// MB), and the answer cannot change while the process runs, so it is computed at
// most once — on first use, so a test binary or a `--seed` run never pays for it.
var exeHash = sync.OnceValue(func() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	// G304: the path is os.Executable() — this process's own binary — not
	// anything a request or config can influence.
	f, err := os.Open(path) //nolint:gosec // G304: path is os.Executable(), not caller-controlled
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }() // read-only; a close error can't affect the hash
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
})

// Read assembles the running binary's build info. imageDigest comes from the
// deployment (validate it with ValidateDigest first); pass "" when none is
// configured.
//
// Fields the toolchain didn't stamp are left empty rather than filled with a
// placeholder: "" reads as "this build carries no such claim", where "unknown"
// would look like a value and could be compared against.
func Read(imageDigest string) Info {
	i := Info{
		Go:               runtime.Version(),
		OS:               runtime.GOOS,
		Arch:             runtime.GOARCH,
		ExecutableSHA256: exeHash(),
		ImageDigest:      imageDigest,
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return i
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			i.Commit = s.Value
		case "vcs.time":
			i.CommitTime = s.Value
		case "vcs.modified":
			i.Modified = s.Value == "true"
		}
	}
	return i
}
