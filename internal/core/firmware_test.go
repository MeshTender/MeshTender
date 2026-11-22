package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/firmware"
)

// firmwareHost serves a one-board index.json, or status when it's non-zero.
func firmwareHost(t *testing.T, status int) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		_, _ = io.WriteString(w, `{"schema":1,"channels":[{"name":"stable","path":"v","firmware":[{
			"env":"heltec_v4_room_server_observer_mqtt","board":"Heltec v4","role":"room_server",
			"chipFamily":"ESP32-S3","latest":"v1.18.0.4313","builds":[{"version":"v1.18.0.4313",
			"ota":{"url":"`+srv.URL+`/v/app.bin"},"full":{"url":"`+srv.URL+`/v/merged.bin","offset":0}}]}]}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestFirmwareManifest: the app host serves an esp-web-tools manifest for a
// published board without a session, cacheably, and answers 404 for anything
// it can't install.
func TestFirmwareManifest(t *testing.T) {
	t.Parallel()
	host := firmwareHost(t, 0)
	cfg := testConfig()
	cfg.FirmwareHost = mustURL(t, host.URL).Host
	_, _, ts, h, _ := splitServerWith(t, true, cfg)

	resp := do(t, ts, h.app, "/firmware/heltec_v4_room_server_observer_mqtt/manifest.json")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.HasPrefix(cc, "public") {
		t.Errorf("Cache-Control = %q, want public", cc)
	}
	var m firmware.Manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m.Version != "v1.18.0.4313" || !m.NewInstallPromptErase || len(m.Builds) != 1 ||
		m.Builds[0].Parts[0].Path != host.URL+"/v/merged.bin" {
		t.Errorf("manifest = %+v", m)
	}

	missing := do(t, ts, h.app, "/firmware/no_such_env/manifest.json")
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Errorf("unknown env: status = %d, want 404", missing.StatusCode)
	}
}

// TestFirmwareManifestHostDown: with nothing cached and the firmware host
// failing, the manifest is unavailable (503), not missing.
func TestFirmwareManifestHostDown(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.FirmwareHost = mustURL(t, firmwareHost(t, http.StatusBadGateway).URL).Host
	_, _, ts, h, _ := splitServerWith(t, true, cfg)
	resp := do(t, ts, h.app, "/firmware/heltec_v4_room_server_observer_mqtt/manifest.json")
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}
