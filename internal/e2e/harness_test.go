//go:build browser

// Package e2e holds browser (end-to-end) tests: they drive a real headless
// Chrome against a live server, validating things Go tests can't — rendered
// DOM, client JS, and (critically) that the strict CSP doesn't break a page
// (CSP violations only surface in the browser console).
//
// The suite is deliberately its own black-box package, depending only on the
// public surface of the app (core.NewServer, the store/auth/identity
// constructors, the /session/callback handoff), so browser tests accumulate
// here without cluttering — or coupling to the internals of — the shipping
// packages. Add a new feature's test as its own <feature>_test.go and reuse the
// e2eServer harness below.
//
// Everything is gated behind the `browser` build tag so the default
// `go test ./...` (and current CI) never needs a browser. Run it with
// `mise run e2e`, which starts a chromedp/headless-shell container. If that
// container isn't reachable the tests t.Skip rather than fail.
//
// Networking note: the browser runs inside the container while the test server
// runs on the Docker host, so the httptest listener binds 0.0.0.0 and the
// browser reaches it via host.docker.internal (both addresses are
// env-overridable for CI — see devtoolsBase/browserHost).
package e2e

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/core"
	"github.com/jleight/meshtender/internal/identity"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/testdb"
)

// TestMain manages the shared testdb container/template for the whole package.
func TestMain(m *testing.M) {
	os.Exit(testdb.RunMain(m))
}

// migrate applies the schema to the template database (cloned per test by
// testdb.Fresh), releasing its connection before the clone.
func migrate(dsn string) error {
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.Migrate(ctx)
}

// The harness talks to a browser it doesn't launch, over addresses that differ
// between local Docker and CI, so both are env-overridable:
//   - devtoolsBase: where headless-shell's DevTools endpoint lives. Locally the
//     published port on the Docker host; in CI the service hostname.
//   - browserHost: how the browser reaches back to this process's test server.
//     Locally host.docker.internal; in CI this step's service hostname.
func devtoolsBase() string { return envOr("E2E_DEVTOOLS_URL", "http://127.0.0.1:9222") }
func browserHost() string  { return envOr("E2E_BROWSER_HOST", "host.docker.internal") }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// e2eServer is a running app server wired to a fresh test DB, addressable both
// from the host (hostURL, for the seed-session Go client) and from the browser
// container (browserURL).
type e2eServer struct {
	store      *store.Store
	ctx        context.Context
	ts         *httptest.Server
	hostURL    string // http://127.0.0.1:PORT  — reachable from this process
	browserURL string // http://host.docker.internal:PORT — reachable from the container
}

// hostLayout names the three surfaces for a test server. The surface whose name
// equals browserHost() is the one the browser can actually reach (that's the
// only alias the container resolves back to this process); everything else is
// reachable only host-side via 127.0.0.1 (the Dispatcher's default → app).
type hostLayout struct{ app, auth, root string }

// defaultHosts puts the APP surface on the browser-reachable host — the common
// case, since most browser tests drive app pages.
func defaultHosts() hostLayout {
	return hostLayout{app: browserHost(), auth: "auth." + browserHost(), root: "root." + browserHost()}
}

// authReachableHosts puts the AUTH surface on the browser-reachable host so a
// browser test can drive auth-host pages (e.g. /account). The app surface moves
// to a name nothing navigates; login()'s /session/callback handoff still works
// because it runs host-side over 127.0.0.1, which the Dispatcher routes to the
// app surface by default.
func authReachableHosts() hostLayout {
	return hostLayout{app: "app." + browserHost(), auth: browserHost(), root: "root." + browserHost()}
}

// newE2EServer stands up the app on a 0.0.0.0 listener so the browser container
// can connect back to it, and returns both address forms. An optional hostLayout
// selects which surface the browser can reach (defaults to the app surface).
func newE2EServer(t *testing.T, layout ...hostLayout) *e2eServer {
	t.Helper()
	hosts := defaultHosts()
	if len(layout) > 0 {
		hosts = layout[0]
	}
	ctx := context.Background()
	st, err := store.New(ctx, testdb.Fresh(t, migrate))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(st.Close)

	var masterKey [32]byte
	_, _ = rand.Read(masterKey[:])
	idSvc, _ := identity.LoadOrCreate(ctx, st, masterKey)

	// The server runs across three distinct hosts so the Dispatcher can tell the
	// surfaces apart. The browser can navigate only the one named browserHost()
	// (host.docker.internal) — the sole alias the container resolves back here; the
	// layout decides which surface that is (app by default, auth for account tests).
	appHost, authHost, rootHost := hosts.app, hosts.auth, hosts.root
	authSvc, err := auth.New(st, st.Pool(), auth.Config{
		RPID: "localhost", RPDisplayName: "test", RPOrigins: []string{"http://localhost"},
		AppHost: appHost, AuthHost: authHost, RootHost: rootHost,
	})
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	srv, err := core.NewServer(st, authSvc, idSvc, &config.Config{
		PrimaryHost: appHost, AuthHost: authHost, RootHost: rootHost,
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}

	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.Listener.Close() // drop the default 127.0.0.1 listener
	ts.Listener = ln
	ts.Start()
	t.Cleanup(ts.Close)

	port := ln.Addr().(*net.TCPAddr).Port
	// ts.URL reflects the 0.0.0.0 listener; rewrite it to loopback so the
	// host-side Go client dials a concrete address.
	ts.URL = fmt.Sprintf("http://127.0.0.1:%d", port)
	return &e2eServer{
		store:      st,
		ctx:        ctx,
		ts:         ts,
		hostURL:    ts.URL,
		browserURL: fmt.Sprintf("http://%s:%d", browserHost(), port),
	}
}

// login creates a user, establishes an authenticated session via the
// /session/callback handoff (the same seam the HTTP handler tests use, but
// reached only through public APIs), and returns the user plus the session
// cookie to inject into the browser.
func (e *e2eServer) login(t *testing.T, username string) (*store.User, *http.Cookie) {
	t.Helper()
	u, err := e.store.CreateUser(e.ctx, username, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	loginID, err := e.store.CreateLogin(e.ctx, u.ID)
	if err != nil {
		t.Fatalf("create login: %v", err)
	}
	code, err := e.store.CreateAuthCode(e.ctx, u.ID, loginID, "/")
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	req, _ := http.NewRequest(http.MethodGet, e.hostURL+"/session/callback?code="+code+"&state=s1", nil)
	req.AddCookie(&http.Cookie{Name: "mt_state", Value: "s1"})
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("session callback: %v", err)
	}
	resp.Body.Close()

	base, _ := url.Parse(e.hostURL)
	for _, c := range jar.Cookies(base) {
		if c.Name == "meshtender_session" {
			return u, c
		}
	}
	t.Fatal("no session cookie after handoff")
	return nil, nil
}

// newRepeater creates a repeater owned by ownerID with a valid MeshCore key.
func (e *e2eServer) newRepeater(t *testing.T, ownerID int64, name string) *store.Repeater {
	t.Helper()
	id, _ := meshcore.GenerateLocalIdentity(rand.Reader)
	rep, err := e.store.CreateRepeater(e.ctx, &store.Repeater{
		OwnerID: ownerID, Name: name, PublicKeyHex: id.String(),
		RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	return rep
}

// consoleWatch records browser console errors and CSP violations seen during a
// test, so a test can assert the page ran cleanly under the strict CSP.
type consoleWatch struct {
	mu   sync.Mutex
	msgs []string
}

func (w *consoleWatch) add(s string) {
	w.mu.Lock()
	w.msgs = append(w.msgs, s)
	w.mu.Unlock()
}

// violations returns any recorded message mentioning a CSP violation.
func (w *consoleWatch) violations() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, m := range w.msgs {
		if strings.Contains(strings.ToLower(m), "content security policy") {
			out = append(out, m)
		}
	}
	return out
}

// assertClean fails if any CSP violation was reported.
func (w *consoleWatch) assertClean(t *testing.T) {
	t.Helper()
	if v := w.violations(); len(v) > 0 {
		t.Fatalf("CSP violation(s) in browser console:\n%s", strings.Join(v, "\n"))
	}
}

// startBrowser connects to the headless-shell container and returns a chromedp
// context, a cancel func, and a console watcher. It skips the test if the
// container isn't reachable.
func startBrowser(t *testing.T) (context.Context, context.CancelFunc, *consoleWatch) {
	t.Helper()
	wsURL := devtoolsWebSocket(t)

	allocCtx, cancelAlloc := chromedp.NewRemoteAllocator(context.Background(), wsURL)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)

	watch := &consoleWatch{}
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *cdplog.EventEntryAdded:
			// CSP violations arrive here with source="security", level="error".
			if e.Entry != nil {
				watch.add(e.Entry.Text)
			}
		case *runtime.EventConsoleAPICalled:
			if e.Type == "error" {
				var parts []string
				for _, a := range e.Args {
					parts = append(parts, string(a.Value))
				}
				watch.add(strings.Join(parts, " "))
			}
		}
	})

	cancel := func() { cancelCtx(); cancelAlloc() }
	return ctx, cancel, watch
}

// devtoolsWebSocket asks the headless-shell container for its browser-level
// DevTools websocket URL, skipping the test if the container is down.
func devtoolsWebSocket(t *testing.T) string {
	t.Helper()
	base := devtoolsBase()
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(base + "/json/version")
	if err != nil {
		t.Skipf("headless-shell not reachable at %s (run `mise run e2e`): %v", base, err)
	}
	defer resp.Body.Close()
	var v struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || v.WebSocketDebuggerURL == "" {
		t.Skipf("headless-shell /json/version malformed: %v", err)
	}
	// The reported ws URL carries the browser's own view of its host (e.g.
	// 127.0.0.1:9222); rewrite it to the host we actually reached it on so the
	// same code works locally (published port) and in CI (service hostname).
	baseURL, err := url.Parse(base)
	if err != nil {
		t.Fatalf("bad E2E_DEVTOOLS_URL %q: %v", base, err)
	}
	wsu, err := url.Parse(v.WebSocketDebuggerURL)
	if err != nil {
		t.Fatalf("bad webSocketDebuggerUrl %q: %v", v.WebSocketDebuggerURL, err)
	}
	wsu.Host = baseURL.Host
	return wsu.String()
}

// setSessionCookie injects the app session cookie for the browser-facing host,
// so the browser is authenticated before it navigates.
func setSessionCookie(c *http.Cookie) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return network.SetCookie(c.Name, c.Value).
			WithDomain(browserHost()).
			WithPath("/").
			WithHTTPOnly(true).
			Do(ctx)
	})
}
