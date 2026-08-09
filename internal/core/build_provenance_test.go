package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/buildinfo"
	"github.com/MeshTender/MeshTender/internal/web"
)

// testDigest is a syntactically valid image digest for the fixture deployments.
var testDigest = "sha256:" + strings.Repeat("b", 64)

// TestVersionIsPublicOnRoot: the endpoint exists for people who cannot sign in,
// so it must answer an anonymous request on the public host.
func TestVersionIsPublicOnRoot(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	resp := do(t, ts, h.root, web.VersionPath)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s on root = %d, want 200", web.VersionPath, resp.StatusCode)
	}

	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The toolchain fields hold for a test binary too; the VCS stamps don't, so
	// asserting on them here would only test the build harness.
	for _, k := range []string{"go", "os", "arch"} {
		if _, ok := got[k]; !ok {
			t.Errorf("payload has no %q: %v", k, got)
		}
	}
}

// TestVersionSkipsSessionMiddleware: the payload is per-process, not per-user, so
// the endpoint is mounted ahead of the session middleware. scs's LoadAndSave adds
// "Vary: Cookie", so its absence is the observable proof (same argument as
// TestStaticSkipsSessionMiddleware).
func TestVersionSkipsSessionMiddleware(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	resp := do(t, ts, h.root, web.VersionPath)
	resp.Body.Close()
	for _, v := range resp.Header.Values("Vary") {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "Cookie") {
				t.Fatalf("%s runs the session middleware (Vary: Cookie); it needs no session", web.VersionPath)
			}
		}
	}
}

// TestVersionReportsTheDeploymentDigest: the digest is the value an auditor
// compares their rebuild against, and it reaches the endpoint only by being
// threaded from config through to the surfaces. That path is easy to break
// silently — the field just goes missing — so assert it end to end.
func TestVersionReportsTheDeploymentDigest(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.ImageDigest = testDigest
	_, _, ts, h, _ := splitServerWith(t, true, cfg)

	resp := do(t, ts, h.root, web.VersionPath)
	defer resp.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["imageDigest"] != testDigest {
		t.Errorf("imageDigest = %v, want %s", got["imageDigest"], testDigest)
	}
}

// TestVersionIsRootHostOnly pins the surface: the app and auth hosts are for
// signed-in users and serve no public discovery, so build provenance lives on the
// one host an outsider is meant to read.
func TestVersionIsRootHostOnly(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	for _, host := range []string{h.app, h.auth} {
		resp := do(t, ts, host, web.VersionPath)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s%s = 200, want it served only on the root host", host, web.VersionPath)
		}
	}
}

// TestAdminBuildPageRequiresCapability: it's an admin page, so it 404s (rather
// than 403s) for everyone else, matching the rest of /admin.
func TestAdminBuildPageRequiresCapability(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	u, plain := appLogin(t, ts, st, ctx, h.app, "nobody")
	// CreateUser bootstraps the FIRST account in a database to full capabilities,
	// so this has to be cleared explicitly — otherwise the fixture is an admin and
	// the test passes for the wrong reason (see TestIdentityBackupRequiresAdmin).
	if err := st.SetCapabilities(ctx, u.ID, false, false); err != nil {
		t.Fatalf("clear capabilities: %v", err)
	}

	resp := do(t, ts, h.app, "/admin/build", plain)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/admin/build without a capability = %d, want 404", resp.StatusCode)
	}
}

// TestAdminBuildPageShowsDigest covers what the page adds over the public JSON:
// the digest an auditor compares their rebuild against.
//
// It deliberately does NOT assert the reproduction commands — the test binary
// carries no VCS stamps, so the page correctly withholds steps that couldn't
// reproduce anything. TestBuildPageOffersStepsForAStampedBuild covers that branch
// with a stamped build instead.
func TestAdminBuildPageShowsDigest(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.ImageDigest = testDigest
	st, ctx, ts, h, _ := splitServerWith(t, true, cfg)

	admin, sess := appLogin(t, ts, st, ctx, h.app, "buildadmin")
	if err := st.SetCapabilities(ctx, admin.ID, true, true); err != nil {
		t.Fatalf("set caps: %v", err)
	}

	body := readBody(t, do(t, ts, h.app, "/admin/build", sess))
	if !strings.Contains(body, testDigest) {
		t.Errorf("page does not show the image digest:\n%s", body)
	}
	// The admin page must not contradict the public endpoint — same source, so
	// the toolchain it names is the one the JSON reports.
	if !strings.Contains(body, runtime.Version()) {
		t.Errorf("page does not name the Go toolchain %q", runtime.Version())
	}
}

// TestBuildPageOffersStepsForAStampedBuild covers the branch a test binary can
// never reach: a build WITH version-control stamps, where the page offers
// reproduction commands. It renders the handler directly against a synthesized
// build rather than through the server, since the stamps are fixed at compile
// time and no fixture can change them.
func TestBuildPageOffersStepsForAStampedBuild(t *testing.T) {
	t.Parallel()
	const commit = "62e30036ee0bfb28f6c1a4a3f5ac5f4a52e4b1c9"
	env, err := web.NewEnv(web.Deps{
		Cfg: testConfig(),
		Build: buildinfo.Info{
			Commit: commit, CommitTime: "2026-08-06T17:47:49-04:00",
			Go: "go1.26.5", OS: "linux", Arch: "arm64",
			ImageDigest: testDigest,
		},
	}, templatesFS)
	if err != nil {
		t.Fatalf("env: %v", err)
	}

	rec := httptest.NewRecorder()
	(&Handlers{Env: env}).pageBuild(rec, httptest.NewRequest(http.MethodGet, "/admin/build", nil))
	body := rec.Body.String()

	if !strings.Contains(body, "git checkout "+commit) {
		t.Errorf("page does not offer the checkout command:\n%s", body)
	}
	// The platform must come from the build, not a hardcoded linux/amd64 — that
	// is the error that would send a verifier off to reproduce the wrong variant.
	if !strings.Contains(body, "mise run image --platform linux/arm64") {
		t.Errorf("page does not offer the rebuild command for the built platform:\n%s", body)
	}
	if !strings.Contains(body, testDigest) {
		t.Errorf("page does not show the image digest:\n%s", body)
	}
}

// TestAdminBuildPageIsLinkedFromAdmin: an admin page nothing links to is one
// nobody finds.
func TestAdminBuildPageIsLinkedFromAdmin(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "adminlinks")
	if err := st.SetCapabilities(ctx, admin.ID, true, true); err != nil {
		t.Fatalf("set caps: %v", err)
	}

	if body := readBody(t, do(t, ts, h.app, "/admin", sess)); !strings.Contains(body, `href="/admin/build"`) {
		t.Errorf("the admin index does not link to the build page:\n%s", body)
	}
}

// TestAdminBuildPageHandlesUnstampedBuild: a `go test`/`go run` binary carries no
// VCS stamps, and the page must say so rather than offering steps that cannot
// reproduce anything. (The test binary is itself such a build.)
func TestAdminBuildPageHandlesUnstampedBuild(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "adminunstamped")
	if err := st.SetCapabilities(ctx, admin.ID, true, true); err != nil {
		t.Fatalf("set caps: %v", err)
	}

	body := readBody(t, do(t, ts, h.app, "/admin/build", sess))
	if strings.Contains(body, "git checkout \"\"") || strings.Contains(body, "git checkout <") {
		t.Errorf("page offers a checkout command with no commit:\n%s", body)
	}
	if !strings.Contains(body, "no version-control stamps") && !strings.Contains(body, "modified working tree") {
		t.Errorf("page does not explain that this build isn't reproducible:\n%s", body)
	}
}
