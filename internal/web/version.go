package web

import (
	"net/http"
	"time"

	"github.com/MeshTender/MeshTender/internal/buildinfo"
)

// VersionPath is the public build-provenance endpoint. It lives on the root
// host, which is the surface an outsider can reach without an account — the
// people this endpoint exists for are exactly the ones who can't sign in.
const VersionPath = "/version"

// SourceURL is where the source of this program is published, and License is the
// terms it is published under.
//
// This is how the deployment satisfies AGPL section 13: anyone interacting with
// the server over a network is entitled to the source it is running, so every
// page footer links here and /version reports it in machine-readable form. It
// pairs with the build provenance below — the source offer names a repository,
// and /version names the commit within it.
//
// Deliberately a constant rather than configuration. An operator running a
// MODIFIED copy owes their users THEIR source, not ours, so this value must
// change in the same commit that changes the code it points at; an env var would
// let a modified deployment keep advertising upstream. Forks: edit this, and see
// TRADEMARKS.md for what else has to change.
const (
	SourceURL = "https://github.com/MeshTender/MeshTender"
	License   = "AGPL-3.0-only"
)

// versionMaxAge is how long a client may cache the answer. The payload only
// changes when the process is replaced, so this is purely about repeat requests;
// it is short enough that a verifier polling across a deploy sees the new build
// promptly.
const versionMaxAge = time.Minute

// versionResponse is the /version payload: the build facts, plus the licensing
// and source-location constants above.
//
// The two are kept in separate types because they are different kinds of claim.
// buildinfo.Info is derived — stamped by the toolchain or measured at runtime —
// while these two are simply asserted by whoever compiled the binary. Embedding
// flattens them into one JSON object without pretending the constants were
// attested by anything.
type versionResponse struct {
	buildinfo.Info
	// Source is where this program's source is published (AGPL §13).
	Source string `json:"source"`
	// License is the SPDX identifier of its terms.
	License string `json:"license"`
}

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
	payload := versionResponse{Info: e.Build, Source: SourceURL, License: License}
	if err := ServeJSONCached(w, r, versionMaxAge, payload); err != nil {
		e.ServerError(w, r, "could not report build information", err)
	}
}
