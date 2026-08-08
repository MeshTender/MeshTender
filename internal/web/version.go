package web

import (
	"net/http"
	"time"
)

// VersionPath is the public build-provenance endpoint. It lives on the root
// host, which is the surface an outsider can reach without an account — the
// people this endpoint exists for are exactly the ones who can't sign in.
const VersionPath = "/version"

// versionMaxAge is how long a client may cache the answer. The payload only
// changes when the process is replaced, so this is purely about repeat requests;
// it is short enough that a verifier polling across a deploy sees the new build
// promptly.
const versionMaxAge = time.Minute

// VersionJSON reports what this binary was built from, so anyone can rebuild the
// named commit and check that they get the same artifact (see "Verifying a
// build" in README.md).
//
// Public and unauthenticated on purpose: a reproducible build that only its
// operator can check against a running server verifies nothing to a third party.
// Nothing here is sensitive — it names a public repository, the toolchain, and
// hashes of artifacts we publish.
//
// A side-effect-free GET, so it satisfies the root host's rule (see
// docs/auth-cross-host.md) that no state-changing request lives there.
func (e *Env) VersionJSON(w http.ResponseWriter, r *http.Request) {
	if err := ServeJSONCached(w, r, versionMaxAge, e.Build); err != nil {
		e.ServerError(w, r, "could not report build information", err)
	}
}
