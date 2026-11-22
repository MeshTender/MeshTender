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

// heltecV4RoomServer is the firmware host's one real board so far.
const heltecV4RoomServer = `{"env":"heltec_v4_room_server_observer_mqtt","board":"Heltec v4","role":"room_server",
	"chipFamily":"ESP32-S3","latest":"v1.18.0.4313","builds":[{"version":"v1.18.0.4313","publishedAt":"2026-11-22T13:47:22Z",
	"ota":{"url":"BASE/v/app.bin"},"full":{"url":"BASE/v/merged.bin","offset":0}}]}`

// firmwareHost serves an index.json listing the given firmware entries on the
// stable channel (BASE in them becomes the host's URL), or status when it's
// non-zero. With no entries it lists heltecV4RoomServer.
func firmwareHost(t *testing.T, status int, entries ...string) *httptest.Server {
	t.Helper()
	if len(entries) == 0 {
		entries = []string{heltecV4RoomServer}
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		body := `{"schema":1,"channels":[{"name":"stable","path":"v","firmware":[` + strings.Join(entries, ",") + `]}]}`
		_, _ = io.WriteString(w, strings.ReplaceAll(body, "BASE", srv.URL))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// firmwareServer is a test server whose firmware host is fw.
func firmwareServer(t *testing.T, fw *httptest.Server) (*httptest.Server, hostEnv, func(string) *http.Cookie) {
	t.Helper()
	cfg := testConfig()
	cfg.FirmwareHost = mustURL(t, fw.URL).Host
	st, ctx, ts, h, _ := splitServerWith(t, true, cfg)
	login := func(name string) *http.Cookie {
		_, sess := appLogin(t, ts, st, ctx, h.app, name)
		return sess
	}
	return ts, h, login
}

// TestFirmwareManifest: the app host serves an esp-web-tools manifest for a
// published board without a session, cacheably, and answers 404 for anything
// it can't install.
func TestFirmwareManifest(t *testing.T) {
	t.Parallel()
	host := firmwareHost(t, 0)
	ts, h, _ := firmwareServer(t, host)

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
	ts, h, _ := firmwareServer(t, firmwareHost(t, http.StatusBadGateway))
	resp := do(t, ts, h.app, "/firmware/heltec_v4_room_server_observer_mqtt/manifest.json")
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestInstallObserverPage: the install page offers every stable board it can
// flash, room servers first and boards by name, leaves out what esp-web-tools
// can't install, and points the install button at the first board's manifest.
func TestInstallObserverPage(t *testing.T) {
	t.Parallel()
	fw := firmwareHost(t, 0,
		`{"env":"heltec_v3_repeater_observer_mqtt","board":"Heltec v3","role":"repeater","chipFamily":"ESP32-S3",
		  "latest":"v1.18.0.4313","builds":[{"version":"v1.18.0.4313","ota":{"url":"BASE/a.bin"},"full":{"url":"BASE/m.bin"}}]}`,
		`{"env":"xiao_s3_room_server_observer_mqtt","board":"Xiao S3","role":"room_server","chipFamily":"ESP32-S3",
		  "latest":"v1.18.0.4313","builds":[{"version":"v1.18.0.4313","ota":{"url":"BASE/a.bin"},"full":{"url":"BASE/m.bin"}}]}`,
		heltecV4RoomServer,
		`{"env":"rak_room_server","board":"RAK4631","role":"room_server","chipFamily":"nRF52840",
		  "latest":"v1.18.0.4313","builds":[{"version":"v1.18.0.4313","ota":{"url":"BASE/a.bin"},"full":{"url":"BASE/m.bin"}}]}`,
		`{"env":"unpublished_room_server","board":"Unpublished","role":"room_server","chipFamily":"ESP32",
		  "latest":"v9","builds":[]}`)
	ts, h, login := firmwareServer(t, fw)

	body := getBody(t, ts, h.app, "/observers/install", login("installer"))
	checkHeadingOutline(t, "observer install", body)
	roleSelect := between(body, `data-testid="firmware-role"`, "</select>")
	if strings.Index(roleSelect, "Room server") > strings.Index(roleSelect, "Repeater") || !strings.Contains(roleSelect, "Repeater") {
		t.Errorf("roles out of order: %s", roleSelect)
	}
	rooms := between(body, `data-testid="firmware-board-room_server"`, "</select>")
	if strings.Index(rooms, "Heltec v4") > strings.Index(rooms, "Xiao S3") || !strings.Contains(rooms, "Xiao S3") {
		t.Errorf("room server boards out of order: %s", rooms)
	}
	for _, unwanted := range []string{"RAK4631", "Unpublished"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("page offers %s, which can't be installed from the browser", unwanted)
		}
	}
	for _, want := range []string{
		`manifest="/firmware/heltec_v4_room_server_observer_mqtt/manifest.json"`,
		`data-published="November 22, 2026"`,
		`/static/esp-web-tools/install-button.`,
		`type="module"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
}

// TestInstallObserverPageCatalogDown: the page still renders, saying the list
// can't be loaded, and offers no install button.
func TestInstallObserverPageCatalogDown(t *testing.T) {
	t.Parallel()
	ts, h, login := firmwareServer(t, firmwareHost(t, http.StatusBadGateway))
	body := getBody(t, ts, h.app, "/observers/install", login("installer"))
	if !strings.Contains(body, `data-testid="firmware-unavailable"`) {
		t.Error("no unavailable notice")
	}
	if strings.Contains(body, "esp-web-install-button") {
		t.Error("offers an install button with nothing to install")
	}
}

func TestInstallObserverPageNeedsSignIn(t *testing.T) {
	t.Parallel()
	ts, h, _ := firmwareServer(t, firmwareHost(t, 0))
	if got := status(t, do(t, ts, h.app, "/observers/install")); got == http.StatusOK {
		t.Errorf("signed-out GET = %d, want a redirect to sign in", got)
	}
}
