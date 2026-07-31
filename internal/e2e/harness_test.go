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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/security"
	"github.com/chromedp/chromedp"
	meshcore "github.com/meshcore-go/meshcore-go"
	"golang.org/x/crypto/bcrypt"

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/core"
	"github.com/jleight/meshtender/internal/identity"
	mailer "github.com/jleight/meshtender/internal/mail"
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

// e2eServer is a running app server wired to a fresh test DB. hostURL reaches it
// from this process (the seed-session Go client); appURL/authURL/rootURL are the
// three surfaces as the browser addresses them — all three resolve back here
// because the container maps every *.browserHost() name to the reachable host
// (see the --host-resolver-rules flag in the `e2e` mise task / CI service).
type e2eServer struct {
	store   *store.Store
	ctx     context.Context
	srv     *core.Server
	ts      *httptest.Server
	hostURL string // https://127.0.0.1:PORT — reachable from this process
	appURL  string // https://app.<browserHost>:PORT  — the product surface
	authURL string // https://auth.<browserHost>:PORT — sign-in + account
	rootURL string // https://root.<browserHost>:PORT — public discovery
	// mail captures what the app would have sent. Account-recovery tests read the
	// links out of it, which is the only way to drive those flows the way a real
	// recipient does — the token exists nowhere else in plaintext.
	mail *captureSender
}

// captureSender records messages instead of delivering them.
type captureSender struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (c *captureSender) Send(_ context.Context, m mailer.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, m)
	return nil
}

// lastLink returns the path of the first recovery link in the most recent message.
// Tests navigate to that path, so the browser follows exactly what a recipient
// would click.
func (c *captureSender) lastLink(t *testing.T) string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sent) == 0 {
		t.Fatal("no mail was sent")
	}
	body := c.sent[len(c.sent)-1].Text
	match := recoveryLinkRe.FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("no recovery link in message body:\n%s", body)
	}
	return match[1]
}

// recoveryLinkRe captures the path of a verification or reset link.
var recoveryLinkRe = regexp.MustCompile(`https?://[^/\s]+(/(?:verify-email|reset)/[A-Za-z0-9_-]+)`)

// Surface hostnames. All three are subdomains of browserHost() so a single
// host-resolver rule (MAP *.<browserHost> <browserHost>) makes every surface
// reachable from the browser at once — the Dispatcher then routes by Host header.
func appHost() string  { return "app." + browserHost() }
func authHost() string { return "auth." + browserHost() }
func rootHost() string { return "root." + browserHost() }

// newE2EServer stands up the app over HTTPS (a self-signed cert) on a 0.0.0.0
// listener so the browser container can connect back to it, across all three
// surfaces at once.
//
// The suite is HTTPS throughout for two reasons: WebAuthn ceremonies require a
// secure context (a plain-HTTP non-localhost origin is not one), and serving TLS
// exercises the same Secure/__Host- cookie path production uses. The browser
// trusts the self-signed cert via Security.setIgnoreCertificateErrors (applied
// for every test in startBrowser); the host-side client skips verification too.
func newE2EServer(t *testing.T) *e2eServer {
	t.Helper()
	ctx := context.Background()
	st, err := store.New(ctx, testdb.Fresh(t, migrate))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(st.Close)

	// One fixed key for both the identity service and the config below, mirroring
	// production where main.go threads a single MESHTENDER_MASTER_KEY through both. A
	// random key here with a zero-valued cfg.MasterKey silently breaks anything that
	// decrypts via the config (the identity backup page, for one).
	masterKey := [32]byte{
		0x54, 0x65, 0x73, 0x74, 0x4d, 0x61, 0x73, 0x74,
		0x65, 0x72, 0x4b, 0x65, 0x79, 0x2d, 0x64, 0x6f,
		0x2d, 0x6e, 0x6f, 0x74, 0x2d, 0x75, 0x73, 0x65,
		0x2d, 0x69, 0x6e, 0x2d, 0x70, 0x72, 0x6f, 0x64,
	}
	idSvc, _ := identity.LoadOrCreate(ctx, st, masterKey)
	sender := &captureSender{}

	// Listen up front so the RP origins can include the concrete (dynamic) port.
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	origin := func(h string) string { return fmt.Sprintf("https://%s:%d", h, port) }

	// The RP ID is the auth host. WebAuthn ceremonies only run there (signup), and
	// its own host is a valid RP ID for that origin. Using the auth host (rather
	// than the bare browserHost parent) also guarantees the RP ID contains a dot,
	// which WebAuthn requires — browserHost() is a single label in CI (the step
	// name, e.g. "e2e"), which would fail go-webauthn's domain validation.
	authSvc, err := auth.New(st, st.Pool(), auth.Config{
		RPID: authHost(), RPDisplayName: "test",
		RPOrigins: []string{origin(appHost()), origin(authHost()), origin(rootHost())},
		AppHost:   appHost(), AuthHost: authHost(), RootHost: rootHost(),
		Secure: true,
		// Mail is reported as configured so the recovery UI is live, while nothing
		// leaves the process. sender captures the links the tests follow.
		Mail: sender, MailEnabled: true,
	})
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	srv, err := core.NewServer(st, authSvc, idSvc, &config.Config{
		PrimaryHost: appHost(), AuthHost: authHost(), RootHost: rootHost(), Secure: true,
		MasterKey: masterKey,
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}

	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.Listener.Close() // drop the default 127.0.0.1 listener
	ts.Listener = ln
	ts.StartTLS()
	t.Cleanup(ts.Close)

	ts.URL = fmt.Sprintf("https://127.0.0.1:%d", port)
	return &e2eServer{
		store:   st,
		ctx:     ctx,
		srv:     srv,
		ts:      ts,
		hostURL: ts.URL,
		appURL:  origin(appHost()),
		authURL: origin(authHost()),
		rootURL: origin(rootHost()),
		mail:    sender,
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
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "s1"})
	// The server uses a self-signed cert; skip verification for the host client.
	client := &http.Client{
		Jar:           jar,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // G402: test client to the harness's own self-signed server
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("session callback: %v", err)
	}
	resp.Body.Close()

	base, _ := url.Parse(e.hostURL)
	for _, c := range jar.Cookies(base) {
		if c.Name == sessionCookieName {
			return u, c
		}
	}
	t.Fatal("no session cookie after handoff")
	return nil, nil
}

// Cookie names as the server emits them over HTTPS (Secure ⇒ __Host- prefix; see
// auth.cookieName). The suite always serves TLS, so these are stable.
const (
	sessionCookieName = "__Host-meshtender_session"
	stateCookieName   = "__Host-mt_state"
)

// setPassword gives an existing account a password, so tests can drive the
// password-dependent flows (sign-in, recovery) without going through the sign-up
// form. MinCost keeps it cheap — these tests aren't measuring bcrypt.
func (e *e2eServer) setPassword(t *testing.T, userID int64, plaintext string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := e.store.SetPassword(e.ctx, userID, string(hash)); err != nil {
		t.Fatalf("set password: %v", err)
	}
}

// waitForLocation polls the browser's URL until it carries prefix.
//
// chromedp has no "wait for this navigation to settle" primitive, and a form submit
// here can redirect more than once (auth host → handoff → app host). Waiting on an
// element instead is a trap: any selector that already matches the current page
// returns immediately, and a Navigate issued while a redirect is still in flight
// fails with ERR_ABORTED.
func waitForLocation(prefix string) chromedp.ActionFunc {
	return func(ctx context.Context) error {
		deadline := time.Now().Add(15 * time.Second)
		var loc string
		for time.Now().Before(deadline) {
			if err := chromedp.Location(&loc).Do(ctx); err == nil && strings.HasPrefix(loc, prefix) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return fmt.Errorf("browser never reached %s (last location %q)", prefix, loc)
	}
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

	// The suite serves a self-signed cert; trust it for every test (also gives
	// WebAuthn its required secure context). This first Run also creates the
	// target the listener above attaches to.
	if err := chromedp.Run(ctx, security.SetIgnoreCertificateErrors(true)); err != nil {
		cancel()
		t.Fatalf("ignore certificate errors: %v", err)
	}
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
		// The session token is store-global; inject it host-only on each surface
		// (a __Host- cookie can't carry a Domain, and cookies ignore port, so the
		// scheme+host is enough) so whichever surface a test drives is authenticated.
		for _, h := range []string{appHost(), authHost(), rootHost()} {
			if err := network.SetCookie(c.Name, c.Value).
				WithURL("https://" + h).
				WithPath("/").
				WithHTTPOnly(true).
				WithSecure(true).
				Do(ctx); err != nil {
				return err
			}
		}
		return nil
	})
}
