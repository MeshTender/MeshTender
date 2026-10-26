package web

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/config"
)

func TestNavSection(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{PrimaryHost: "app.example", AuthHost: "auth.example", RootHost: "example"}
	for _, tc := range []struct{ url, want string }{
		{"https://app.example/", NavDashboard},
		{"https://app.example/repeaters", NavRepeaters},
		{"https://app.example/repeaters/abc/console", NavRepeaters},
		{"https://app.example/repeatersx", ""},
		{"https://app.example/companions/abc", NavCompanions},
		{"https://app.example/orgs/wny/nets/3", NavOrganizations},
		{"https://app.example/admin/users", NavAdmin},
		{"https://app.example/invite/tok", ""},
		{"https://example/orgs/wny", NavOrganizations},
		{"https://example/docs", NavDocs},
		{"https://example/", ""},
		{"https://auth.example/account", ""},
	} {
		if got := navSection(cfg, httptest.NewRequest(http.MethodGet, tc.url, nil)); got != tc.want {
			t.Errorf("navSection(%s) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

// renderLayout renders the error page in a layout for url, signed in as
// "alice" when signedIn.
func renderLayout(t *testing.T, layout, url string, signedIn bool) string {
	t.Helper()
	e := testEnv(t)
	if signedIn {
		e.Renderer.userInfo = func(context.Context) (string, bool, string, bool) { return "alice", false, "", true }
	}
	rec := httptest.NewRecorder()
	e.Renderer.render(rec, httptest.NewRequest(http.MethodGet, url, nil), http.StatusNotFound, "error.html",
		map[string]any{"Layout": layout, "Status": http.StatusNotFound, "Title": "Page not found", "Message": "gone"})
	return rec.Body.String()
}

// TestTopNavMarksTheCurrentSection: the top nav underlines the section the page
// is in (and only that one), and a signed-in user gets the same app sections
// on the public host as on the app host.
func TestTopNavMarksTheCurrentSection(t *testing.T) {
	t.Parallel()
	active := regexp.MustCompile(`<li class="nav-item active">\s*<a class="nav-link" href="([^"]+)" aria-current="page"`)

	body := renderLayout(t, "base", "https://app.example/repeaters/abc", true)
	if m := active.FindAllStringSubmatch(body, -1); len(m) != 1 || !strings.HasSuffix(m[0][1], "/repeaters") {
		t.Errorf("app /repeaters/abc: active items %v, want just Repeaters", m)
	}

	root := renderLayout(t, "rootbase", "https://example/orgs/wny", true)
	for _, want := range []string{"/repeaters\"", "/companions\""} {
		if !strings.Contains(root, want) {
			t.Errorf("signed in on the public host, the nav lacks %s", want)
		}
	}
	if m := active.FindAllStringSubmatch(root, -1); len(m) != 1 || !strings.HasSuffix(m[0][1], "/orgs") {
		t.Errorf("public /orgs/wny: active items %v, want just Organizations", m)
	}

	out := renderLayout(t, "landingbase", "https://example/", false)
	if strings.Contains(out, "/repeaters\"") || !strings.Contains(out, "Sign in") {
		t.Error("signed out, the nav should offer sign-in and no app sections")
	}
}

// TestEveryLayoutShowsRedirectFlashes: a redirect's ?ok= / ?error= message
// appears on every layout, escaped, without the page doing anything.
func TestEveryLayoutShowsRedirectFlashes(t *testing.T) {
	t.Parallel()
	for _, layout := range []string{"base", "rootbase", "authbase", "landingbase"} {
		body := renderLayout(t, layout, "https://app.example/x?ok=Saved+%3Cb%3E&error=Nope", false)
		if !strings.Contains(body, `data-testid="flash-ok"`) || !strings.Contains(body, "Saved &lt;b&gt;") {
			t.Errorf("%s: no escaped ok flash", layout)
		}
		if !strings.Contains(body, `data-testid="flash-error"`) || !strings.Contains(body, "Nope") {
			t.Errorf("%s: no error flash", layout)
		}
	}
}

// TestHandlersLeaveRedirectFlashesToTheLayout: the layout shows ?ok= and
// ?error=, so a handler reading them into page data would show the message
// twice. (A form's own validation error is page data under another source.)
func TestHandlersLeaveRedirectFlashesToTheLayout(t *testing.T) {
	t.Parallel()
	readsFlash := regexp.MustCompile(`Query\(\)\.Get\("(ok|error)"\)|\bq\.Get\("(ok|error)"\)`)
	for _, dir := range []string{"../core", "../auth", "../marketing"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			b, err := os.ReadFile(path) //nolint:gosec // G304: walking our own source tree
			if err != nil {
				return err
			}
			if m := readsFlash.Find(b); m != nil {
				t.Errorf("%s reads %s; the layout already shows it (see the flash template)", path, m)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
