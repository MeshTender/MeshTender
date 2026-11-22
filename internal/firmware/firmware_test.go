package firmware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// indexJSON is a trimmed copy of the firmware host's real index.json, with its
// image URLs on base.
func indexJSON(base string) string {
	return strings.ReplaceAll(`{
  "schema": 1,
  "generatedAt": "2026-11-22T19:56:05Z",
  "channels": [{
    "name": "stable",
    "path": "v",
    "firmware": [{
      "env": "heltec_v4_room_server_observer_mqtt",
      "board": "Heltec v4",
      "role": "room_server",
      "chipFamily": "ESP32-S3",
      "latest": "v1.18.0.4313",
      "builds": [{
        "version": "v1.18.0.4313",
        "baseVersion": "v1.18.0",
        "build": 4313,
        "hash": "fe351d9f",
        "publishedAt": "2026-11-22T13:47:22Z",
        "ota": {"url": "BASE/v/heltec_v4_room_server_observer_mqtt-v1.18.0.4313-fe351d9f.bin", "size": 1722192, "sha256": "7ad3"},
        "full": {"url": "BASE/v/heltec_v4_room_server_observer_mqtt-v1.18.0.4313-fe351d9f-merged.bin", "size": 1787728, "sha256": "ebd3", "offset": 0}
      }]
    }]
  }]
}`, "BASE", base)
}

// fakeHost serves body (or status, when non-zero) at /index.json and counts
// the requests.
type fakeHost struct {
	srv    *httptest.Server
	hits   atomic.Int32
	status atomic.Int32
	body   atomic.Value // string
}

func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	h := &fakeHost{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.json" {
			http.NotFound(w, r)
			return
		}
		h.hits.Add(1)
		if s := h.status.Load(); s != 0 {
			w.WriteHeader(int(s))
			return
		}
		_, _ = w.Write([]byte(h.body.Load().(string)))
	}))
	t.Cleanup(h.srv.Close)
	h.body.Store(indexJSON(h.srv.URL))
	return h
}

// testCatalog returns a catalog on h whose clock the test advances.
func testCatalog(t *testing.T, h *fakeHost) (*Catalog, *time.Time) {
	t.Helper()
	c, err := NewCatalog(h.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	return c, &now
}

func TestCatalogCachesUntilTTL(t *testing.T) {
	t.Parallel()
	h := newFakeHost(t)
	c, now := testCatalog(t, h)
	ctx := context.Background()

	for range 3 {
		if _, err := c.Index(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := h.hits.Load(); got != 1 {
		t.Fatalf("fetched %d times within the TTL, want 1", got)
	}
	*now = now.Add(indexTTL)
	if _, err := c.Index(ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.hits.Load(); got != 2 {
		t.Fatalf("fetched %d times after the TTL, want 2", got)
	}
}

// A refresh that fails keeps serving the last good index, and waits
// retryAfterError before trying again rather than fetching on every call.
func TestCatalogServesPreviousIndexWhenRefreshFails(t *testing.T) {
	t.Parallel()
	h := newFakeHost(t)
	c, now := testCatalog(t, h)
	ctx := context.Background()
	first, err := c.Index(ctx)
	if err != nil {
		t.Fatal(err)
	}

	h.status.Store(http.StatusBadGateway)
	*now = now.Add(indexTTL)
	got, err := c.Index(ctx)
	if err != nil {
		t.Fatalf("refresh failure surfaced: %v", err)
	}
	if got != first {
		t.Error("didn't serve the previous index")
	}
	if _, err := c.Index(ctx); err != nil {
		t.Fatal(err)
	}
	if hits := h.hits.Load(); hits != 2 {
		t.Fatalf("fetched %d times, want 2 (no retry before retryAfterError)", hits)
	}

	h.status.Store(0)
	*now = now.Add(retryAfterError)
	if _, err := c.Index(ctx); err != nil {
		t.Fatal(err)
	}
	if hits := h.hits.Load(); hits != 3 {
		t.Fatalf("fetched %d times, want 3 (retried after retryAfterError)", hits)
	}
}

func TestCatalogErrorsWithNothingCached(t *testing.T) {
	t.Parallel()
	h := newFakeHost(t)
	h.status.Store(http.StatusNotFound)
	c, _ := testCatalog(t, h)
	if _, err := c.Index(context.Background()); err == nil {
		t.Fatal("no error for a missing index")
	}
}

func TestCatalogRefusesOversizedIndex(t *testing.T) {
	t.Parallel()
	h := newFakeHost(t)
	h.body.Store(strings.Repeat(" ", maxIndexBytes+1))
	c, _ := testCatalog(t, h)
	if _, err := c.Index(context.Background()); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v, want the size limit", err)
	}
}

// One caller giving up mustn't fail the shared fetch.
func TestCatalogFetchIgnoresCallerCancellation(t *testing.T) {
	t.Parallel()
	h := newFakeHost(t)
	c, _ := testCatalog(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Index(ctx); err != nil {
		t.Fatalf("canceled caller failed the fetch: %v", err)
	}
}

func TestNewCatalogNeedsAbsoluteURL(t *testing.T) {
	t.Parallel()
	for _, u := range []string{"", "firmware.meshtender.com", "/index", "ftp://firmware.meshtender.com"} {
		if _, err := NewCatalog(u); err == nil {
			t.Errorf("NewCatalog(%q) accepted", u)
		}
	}
	c, err := NewCatalog("https://firmware.meshtender.com/")
	if err != nil {
		t.Fatal(err)
	}
	if c.indexURL != "https://firmware.meshtender.com/index.json" {
		t.Errorf("indexURL = %q", c.indexURL)
	}
}

func TestParseIndexRefusesImagesOffTheFirmwareHost(t *testing.T) {
	t.Parallel()
	c, err := NewCatalog("https://firmware.meshtender.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseIndex([]byte(indexJSON("https://firmware.meshtender.com")), c.base); err != nil {
		t.Fatalf("valid index refused: %v", err)
	}
	for name, base := range map[string]string{
		"other host":    "https://evil.example",
		"plain http":    "http://firmware.meshtender.com",
		"lookalike":     "https://firmware.meshtender.com.evil.example",
		"with userinfo": "https://firmware.meshtender.com@evil.example",
	} {
		if _, err := parseIndex([]byte(indexJSON(base)), c.base); err == nil {
			t.Errorf("%s: index with images on %s accepted", name, base)
		}
	}
	other := strings.Replace(indexJSON("https://firmware.meshtender.com"), `"schema": 1`, `"schema": 2`, 1)
	if _, err := parseIndex([]byte(other), c.base); err == nil {
		t.Error("schema 2 accepted")
	}
}

func TestLookupAndLatestBuild(t *testing.T) {
	t.Parallel()
	c, err := NewCatalog("https://firmware.meshtender.com")
	if err != nil {
		t.Fatal(err)
	}
	idx, err := parseIndex([]byte(indexJSON("https://firmware.meshtender.com")), c.base)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := idx.Lookup(StableChannel, "heltec_v4_room_server_observer_mqtt")
	if !ok {
		t.Fatal("env not found")
	}
	b, ok := f.LatestBuild()
	if !ok || b.Build != 4313 || b.Full.Size != 1787728 {
		t.Fatalf("LatestBuild = %+v, %v", b, ok)
	}
	if _, ok := idx.Lookup("beta", "heltec_v4_room_server_observer_mqtt"); ok {
		t.Error("found the env on a channel that doesn't list it")
	}
	if _, ok := idx.Lookup(StableChannel, "nope"); ok {
		t.Error("found an unknown env")
	}
	f.Latest = "v9.9.9"
	if _, ok := f.LatestBuild(); ok {
		t.Error("LatestBuild found a version that isn't published")
	}
}

// The manifest asks before erasing (esp-web-tools erases a new install
// unconditionally otherwise), skips the Improv wait, and flashes the merged
// image at its offset.
func TestWebToolsManifest(t *testing.T) {
	t.Parallel()
	c, err := NewCatalog("https://firmware.meshtender.com")
	if err != nil {
		t.Fatal(err)
	}
	idx, err := parseIndex([]byte(indexJSON("https://firmware.meshtender.com")), c.base)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := idx.Lookup(StableChannel, "heltec_v4_room_server_observer_mqtt")
	b, _ := f.LatestBuild()
	m, err := WebToolsManifest(f, b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"MeshTender observer (Heltec v4, room server)","version":"v1.18.0.4313",` +
		`"new_install_prompt_erase":true,"new_install_improv_wait_time":0,` +
		`"builds":[{"chipFamily":"ESP32-S3","parts":[{"path":"https://firmware.meshtender.com/v/heltec_v4_room_server_observer_mqtt-v1.18.0.4313-fe351d9f-merged.bin","offset":0}]}]}`
	if string(got) != want {
		t.Errorf("manifest =\n%s\nwant\n%s", got, want)
	}

	nrf := f
	nrf.ChipFamily = "nRF52840"
	if _, err := WebToolsManifest(nrf, b); !errors.Is(err, ErrNotFlashable) {
		t.Errorf("nRF52 manifest err = %v, want ErrNotFlashable", err)
	}
	noFull := b
	noFull.Full = Image{}
	if _, err := WebToolsManifest(f, noFull); !errors.Is(err, ErrNotFlashable) {
		t.Errorf("no merged image err = %v, want ErrNotFlashable", err)
	}
}
