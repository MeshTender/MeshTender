package web

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/config"
)

// layoutWithCartoKey is the part of base.html this test cares about: the root
// element carrying the key for basemap.js to read.
const layoutWithCartoKey = `{{define "base"}}<html data-carto-key="{{.CartoKey}}"></html>{{end}}`

// renderCartoLayout renders a minimal layout through the real Renderer with the
// given configured key, and returns the body.
func renderCartoLayout(t *testing.T, key string) string {
	t.Helper()
	rn := &Renderer{
		cfg: &config.Config{
			PrimaryHost: "app.example", AuthHost: "auth.example", RootHost: "example",
			CartoKey: key,
		},
		pages: map[string]*template.Template{
			"map.html": template.Must(template.New("base").Parse(layoutWithCartoKey)),
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://app.example/", nil)
	rn.Render(rec, req, "map.html", map[string]any{"Layout": "base"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

// TestCartoKeyReachesTheLayout: the configured key has to arrive in the rendered
// page, because that attribute is the only channel basemap.js has for it. It is
// injected in Render's common data rather than by the map handlers, so every
// surface gets it without each map page remembering to pass it.
func TestCartoKeyReachesTheLayout(t *testing.T) {
	t.Parallel()
	body := renderCartoLayout(t, "abc123")
	if !strings.Contains(body, `data-carto-key="abc123"`) {
		t.Fatalf("configured CARTO key missing from rendered page: %s", body)
	}
}

// TestCartoKeyAbsentRendersEmptyAttribute: with no key configured the attribute is
// present but empty, which is what basemap.js reads as "omit the key parameter".
// The failure this pins is a nil/missing map entry rendering as "<no value>", which
// would be appended to every tile URL as a literal key.
func TestCartoKeyAbsentRendersEmptyAttribute(t *testing.T) {
	t.Parallel()
	body := renderCartoLayout(t, "")
	if !strings.Contains(body, `data-carto-key=""`) {
		t.Fatalf("unconfigured CARTO key should render an empty attribute: %s", body)
	}
	if strings.Contains(body, "no value") {
		t.Fatalf("CartoKey rendered as a missing template field: %s", body)
	}
}

// TestMapPagesLoadBasemapFirst: meshmap.js and regionmap.js both call
// meshBaseLayers, which now lives in basemap.js. A page that loads one of them
// without basemap.js throws a ReferenceError at map-init time and renders a blank
// container — no build error, no failing Go test, and nothing in the CSP report.
// Since map pages span all three surfaces, pin the include structurally rather
// than trusting the next one to remember it.
func TestMapPagesLoadBasemapFirst(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)

	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		rel, _ := filepath.Rel(root, path)

		base := strings.Index(src, "/static/basemap.js")
		for _, dep := range []string{"/static/meshmap.js", "/static/regionmap.js"} {
			at := strings.Index(src, dep)
			if at < 0 {
				continue
			}
			if base < 0 {
				t.Errorf("%s loads %s but not /static/basemap.js, which defines the "+
					"meshBaseLayers it calls", rel, dep)
				continue
			}
			if base > at {
				t.Errorf("%s loads /static/basemap.js after %s; it must come first so "+
					"meshBaseLayers is defined when the map initializes", rel, dep)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}

// TestBasemapTileURLOmitsEmptyKey guards the one branch in basemap.js that a Go
// test can reach: the tile URL must not carry a "?key=" when no key is configured.
func TestBasemapTileURLOmitsEmptyKey(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "internal/web/static/basemap.js"))
	if err != nil {
		t.Fatalf("read basemap.js: %v", err)
	}
	src := string(b)
	if !strings.Contains(src, `key ? url + "?key=" + encodeURIComponent(key) : url`) {
		t.Fatal("basemap.js no longer guards the key parameter on a non-empty key; " +
			"an unset MESHTENDER_CARTO_KEY must omit ?key= entirely, not send it empty")
	}
	if !strings.Contains(src, `getAttribute("data-carto-key")`) {
		t.Fatal("basemap.js no longer reads the key from <html data-carto-key>")
	}
}
