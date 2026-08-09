package core

import (
	"net/http"

	"github.com/MeshTender/MeshTender/internal/web"
)

// The admin view of build provenance. It renders the same facts as the public
// web.VersionPath endpoint — deliberately, so an operator reads exactly what an
// outside auditor reads — plus the commands to reproduce this build.
//
// The reproduction steps are derived from the running build rather than written
// out in the template: a hardcoded `--platform linux/amd64` would quietly be
// wrong on an arm64 deployment, and that is the kind of error that makes a
// verifier conclude the artifact doesn't match when the instructions were simply
// aimed at the wrong target.

// pageBuild renders the build-provenance page.
func (s *Handlers) pageBuild(w http.ResponseWriter, r *http.Request) {
	b := s.Build
	// Only offer a checkout command when there is a commit to check out; a
	// from-source run has no VCS stamps, and `git checkout ""` is worse than
	// showing nothing.
	var checkout string
	if b.Commit != "" {
		checkout = "git checkout " + b.Commit
	}
	s.Render(w, r, "admin_build.html", map[string]any{
		"Build":       b,
		"Checkout":    checkout,
		"ImageCmd":    "mise run image --platform " + b.OS + "/" + b.Arch,
		"VersionPath": web.VersionPath,
		// Reproducible drives the page's headline: a dirty or unstamped build
		// can't be reproduced from a commit, and saying so plainly beats
		// printing steps that will not produce a matching digest.
		"Reproducible": b.Reproducible(),
	})
}
