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

// TestEveryLayoutCarriesTheWorkerURL: MapLibre's CSP build will not start without
// an explicit worker URL, and the URL is content-fingerprinted, so it can only come
// from the server. It rides on the root element next to the CARTO key, which means
// every layout has to carry it — a map rendered under a layout that forgot it dies
// with "Failed to initialize worker" and paints nothing.
func TestEveryLayoutCarriesTheWorkerURL(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join(moduleRoot(t), "internal/web/templates/base.html"))
	if err != nil {
		t.Fatalf("read base.html: %v", err)
	}
	src := string(b)

	roots := strings.Count(src, "<html lang=")
	workers := strings.Count(src, `data-maplibre-worker="{{ asset "/static/maplibre-gl-worker.js" }}"`)
	if roots == 0 {
		t.Fatal("base.html declares no <html> root; this test can no longer see the layouts")
	}
	if workers != roots {
		t.Errorf("base.html has %d layout roots but %d carry data-maplibre-worker; "+
			"every layout needs it or a map rendered under it cannot start its worker", roots, workers)
	}
}

// TestMapPagesLoadTheMapStack: meshmap.js and regionmap.js both build on
// meshCreateMap, which lives in basemap.js, which in turn needs the maplibregl
// global — the "map-scripts" define loads both in that order. A page that loads a
// map module without it throws a ReferenceError at map-init time and renders a
// blank container — no build error, no failing Go test, and nothing in the CSP
// report. Since map pages span all three surfaces, pin the include structurally
// rather than trusting the next one to remember it.
func TestMapPagesLoadTheMapStack(t *testing.T) {
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

		// base.html defines the shared blocks; it is not a page that renders a map.
		if rel == filepath.Join("internal", "web", "templates", "base.html") {
			return nil
		}

		stack := strings.Index(src, `{{template "map-scripts"}}`)
		for _, dep := range []string{"/static/meshmap.js", "/static/regionmap.js"} {
			at := strings.Index(src, dep)
			if at < 0 {
				continue
			}
			if stack < 0 {
				t.Errorf(`%s loads %s but not {{template "map-scripts"}}, which loads the `+
					`maplibregl global and the meshCreateMap it calls`, rel, dep)
				continue
			}
			if stack > at {
				t.Errorf(`%s loads {{template "map-scripts"}} after %s; it must come first so `+
					`meshCreateMap is defined when the map initializes`, rel, dep)
			}
		}

		// Terra Draw is only shipped where a shape is actually drawn, so the page that
		// draws one has to ask for it explicitly.
		if strings.Contains(src, "initRegionArea(") &&
			!strings.Contains(src, `{{template "map-draw-scripts"}}`) {
			t.Errorf(`%s calls initRegionArea but does not include {{template "map-draw-scripts"}}, `+
				`so terraDraw is undefined when the editor starts`, rel)
		}
		// ...and a page that does not draw should not pay for it.
		if !strings.Contains(src, "initRegionArea(") &&
			strings.Contains(src, `{{template "map-draw-scripts"}}`) {
			t.Errorf("%s ships Terra Draw but draws nothing; drop the include", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}

// TestBasemapKeysEveryCartoRequest guards the branches in basemap.js that a Go test
// can reach.
//
// The key has to be attached by a transformRequest rather than written into the
// style URL: a CARTO style names its tiles, sprite and glyphs as absolute URLs that
// carry no key of their own, so keying only the style leaves every request that
// actually fetches map data unkeyed — and CARTO answer those with watermarked
// output rather than an error, which is exactly the kind of failure nobody notices.
func TestBasemapKeysEveryCartoRequest(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join(moduleRoot(t), "internal/web/static/basemap.js"))
	if err != nil {
		t.Fatalf("read basemap.js: %v", err)
	}
	src := string(b)

	if !strings.Contains(src, "transformRequest: transformRequest") {
		t.Error("basemap.js no longer registers a transformRequest on the map, so the style's " +
			"tile/sprite/glyph requests would go out unkeyed")
	}
	if !strings.Contains(src, `if (!key) return { url: url };`) {
		t.Error("basemap.js no longer short-circuits on an empty key; an unset " +
			"MESHTENDER_CARTO_KEY must omit ?key= entirely, not send it empty")
	}
	if !strings.Contains(src, `getAttribute("data-carto-key")`) {
		t.Error("basemap.js no longer reads the key from <html data-carto-key>")
	}
	if !strings.Contains(src, `getAttribute("data-maplibre-worker")`) {
		t.Error("basemap.js no longer reads the worker URL from <html data-maplibre-worker>")
	}
	// The raster tiles are deprecated upstream; a .png tile URL here means someone
	// reintroduced them, and they would be blocked by the CSP now that img-src no
	// longer names CARTO.
	if strings.Contains(src, "basemaps.cartocdn.com/rastertiles") || strings.Contains(src, "{z}/{x}/{y}") {
		t.Error("basemap.js references CARTO raster tiles, which are deprecated and no longer " +
			"allowed by the CSP (img-src is 'self')")
	}
}
