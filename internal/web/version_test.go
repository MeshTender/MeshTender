package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jleight/meshtender/internal/buildinfo"
)

func testBuild() buildinfo.Info {
	return buildinfo.Info{
		Commit: "c0ffeec0ffeec0ffeec0ffeec0ffeec0ffeec0ff", CommitTime: "2026-08-06T17:47:49-04:00",
		Go: "go1.26.5", OS: "linux", Arch: "amd64",
		ExecutableSHA256: strings.Repeat("a", 64),
		ImageDigest:      "sha256:" + strings.Repeat("b", 64),
	}
}

// TestVersionJSONReportsTheBuild is the contract an auditor scripts against:
// the endpoint answers with the build info as JSON, unauthenticated.
func TestVersionJSONReportsTheBuild(t *testing.T) {
	t.Parallel()
	e := &Env{Build: testBuild()}
	rec := httptest.NewRecorder()
	e.VersionJSON(rec, httptest.NewRequest(http.MethodGet, VersionPath, nil))

	res := rec.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}

	var got buildinfo.Info
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != testBuild() {
		t.Errorf("reported %+v, want %+v", got, testBuild())
	}
}

// TestVersionJSONRevalidatesCheaply: the payload is fixed for the life of the
// process, so a repeat request should be answerable with a 304 rather than
// re-sending it.
func TestVersionJSONRevalidatesCheaply(t *testing.T) {
	t.Parallel()
	e := &Env{Build: testBuild()}

	first := httptest.NewRecorder()
	e.VersionJSON(first, httptest.NewRequest(http.MethodGet, VersionPath, nil))
	etag := first.Result().Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on the first response")
	}

	req := httptest.NewRequest(http.MethodGet, VersionPath, nil)
	req.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	e.VersionJSON(second, req)
	if second.Code != http.StatusNotModified {
		t.Errorf("revalidated request = %d, want 304", second.Code)
	}
}

// TestVersionJSONOmitsUnstampedFields: a from-source build must not report a
// commit it doesn't have. An auditor reading "commit": "" could otherwise take
// it as a claim rather than as an absence.
func TestVersionJSONOmitsUnstampedFields(t *testing.T) {
	t.Parallel()
	e := &Env{Build: buildinfo.Info{Go: "go1.26.5", OS: "darwin", Arch: "arm64"}}
	rec := httptest.NewRecorder()
	e.VersionJSON(rec, httptest.NewRequest(http.MethodGet, VersionPath, nil))

	body := rec.Body.String()
	for _, k := range []string{"commit", "commitTime", "executableSHA256", "imageDigest"} {
		if strings.Contains(body, `"`+k+`"`) {
			t.Errorf("unstamped %s should be omitted, got %s", k, body)
		}
	}
	if !strings.Contains(body, `"go":"go1.26.5"`) {
		t.Errorf("toolchain should always be reported, got %s", body)
	}
}
