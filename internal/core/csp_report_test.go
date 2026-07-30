package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/identity"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// cspServer stands up the full split-host server AND hands back the *Server, which
// splitServer doesn't — the report pipeline can only be tested end to end by running
// the collector that Server owns.
func cspServer(t *testing.T) (*store.Store, context.Context, *httptest.Server, hostEnv, *Server) {
	t.Helper()
	st, ctx := coreStore(t)
	idSvc, err := identity.LoadOrCreate(ctx, st, testMasterKey)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	authSvc, err := auth.New(st, st.Pool(), testAuthConfig())
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	srv, err := NewServer(st, authSvc, idSvc, testConfig())
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	port := mustURL(t, ts.URL).Port()
	hp := func(h string) string { return h + ":" + port }
	env := hostEnv{auth: hp(testAuthHost), app: hp(testAppHost), root: hp(testRootHost), www: hp(testWWWHost)}
	return st, ctx, ts, env, srv
}

// postReport posts a raw body to the violation-report endpoint on the given host.
func postReport(t *testing.T, ts *httptest.Server, host, contentType, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+web.CSPReportPath, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = host
	req.Header.Set("Content-Type", contentType)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatalf("post report to %s: %v", host, err)
	}
	return resp
}

// flushReports runs the collector's drain-and-write path to completion. Cancelling
// the context immediately is exactly what shutdown does, and the drain branch writes
// whatever is queued — so this exercises the real flush rather than a test-only hook.
func flushReports(t *testing.T, srv *Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.CollectCSPReports(ctx)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the CSP collector didn't finish draining")
	}
}

func legacyReportBody(documentURL, blocked string) string {
	return `{"csp-report":{"document-uri":"` + documentURL + `","blocked-uri":"` + blocked +
		`","effective-directive":"script-src-elem","disposition":"enforce"}}`
}

// TestCSPReportEndpointIsServedOnEveryHost: a violation on the auth host has to be
// reportable to the auth host, or the delivery is cross-origin and needs CORS. The
// endpoint is registered via SharedRoutes for exactly this reason, and it must stay
// reachable without a session.
func TestCSPReportEndpointIsServedOnEveryHost(t *testing.T) {
	t.Parallel()
	_, _, ts, env, _ := cspServer(t)

	for _, host := range []string{env.app, env.auth, env.root} {
		resp := postReport(t, ts, host, "application/csp-report",
			legacyReportBody("http://"+host+"/", "inline"))
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("%s: status = %d, want 204", host, resp.StatusCode)
		}
		// Reports arrive with no credentials; a session cookie here would mean the
		// endpoint sits behind the session middleware and mints a row per report.
		for _, c := range resp.Cookies() {
			if c.Name == "meshtender_session" {
				t.Errorf("%s: the report endpoint set a session cookie", host)
			}
		}
	}

	// The www host is the exception, and deliberately so: the dispatcher 301s
	// everything on it to the apex, so no document is ever served there and no
	// violation can originate there. Browsers do not follow redirects when
	// delivering reports, so if www ever starts serving pages this assertion is
	// where the missing endpoint surfaces.
	resp := postReport(t, ts, env.www, "application/csp-report",
		legacyReportBody("http://"+env.www+"/", "inline"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("www host: status = %d, want 301 (www serves no pages, so it needs no "+
			"report endpoint — if it now serves content, register one)", resp.StatusCode)
	}
}

// TestCSPReportReachesTheDatabase is the end-to-end proof: a posted report survives
// the endpoint, the collector's queue, the drain on shutdown, and the upsert.
func TestCSPReportReachesTheDatabase(t *testing.T) {
	t.Parallel()
	st, ctx, ts, env, srv := cspServer(t)

	body := legacyReportBody("http://"+env.app+"/repeaters", "https://evil.example.com/x.js")
	for i := 0; i < 3; i++ {
		postReport(t, ts, env.app, "application/csp-report", body).Body.Close()
	}
	flushReports(t, srv)

	rows, err := st.ListCSPReports(ctx, "", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("stored %d rows, want 1 aggregated row: %+v", len(rows), rows)
	}
	got := rows[0]
	if got.Hits != 3 {
		t.Errorf("Hits = %d, want 3", got.Hits)
	}
	if got.DocumentPath != "/repeaters" {
		t.Errorf("DocumentPath = %q, want /repeaters", got.DocumentPath)
	}
	if got.BlockedURI != "https://evil.example.com" {
		t.Errorf("BlockedURI = %q, want the host without the path", got.BlockedURI)
	}
	if got.Directive != "script-src-elem" || got.Source != store.CSPReportSourcePage {
		t.Errorf("Directive/Source = %q/%q", got.Directive, got.Source)
	}
}

// TestCSPReportEndpointSurvivesCrossSiteLabel: the CSRF layer must not eat
// browser-generated reports. Reports are delivered out-of-band from the document, and
// nothing pins down the Sec-Fetch-Site value on that request — if it were blocked,
// reporting would fail silently and look like there was nothing to report.
func TestCSPReportEndpointSurvivesCrossSiteLabel(t *testing.T) {
	t.Parallel()
	st, ctx, ts, env, srv := cspServer(t)

	req, err := http.NewRequest(http.MethodPost, ts.URL+web.CSPReportPath,
		strings.NewReader(legacyReportBody("http://"+env.app+"/x", "inline")))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = env.app
	req.Header.Set("Content-Type", "application/csp-report")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 — the CSRF layer is discarding violation reports", resp.StatusCode)
	}
	flushReports(t, srv)
	rows, _ := st.ListCSPReports(ctx, "", 10)
	if len(rows) != 1 {
		t.Errorf("stored %d rows, want the report recorded", len(rows))
	}
}

// TestCSPReportsFromDifferentHostsStayDistinct: one collector serves every surface,
// so the host has to be part of a violation's identity — the same inline violation on
// the auth and app hosts is two different problems in two different templates.
func TestCSPReportsFromDifferentHostsStayDistinct(t *testing.T) {
	t.Parallel()
	st, ctx, ts, env, srv := cspServer(t)

	postReport(t, ts, env.app, "application/csp-report",
		legacyReportBody("http://"+env.app+"/x", "inline")).Body.Close()
	postReport(t, ts, env.auth, "application/csp-report",
		legacyReportBody("http://"+env.auth+"/x", "inline")).Body.Close()
	flushReports(t, srv)

	rows, err := st.ListCSPReports(ctx, "", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("stored %d rows, want 2 (one per host): %+v", len(rows), rows)
	}
	hosts := map[string]bool{}
	for _, r := range rows {
		hosts[r.Host] = true
	}
	if !hosts[testAppHost] || !hosts[testAuthHost] {
		t.Errorf("hosts = %v, want both the app and auth hosts", hosts)
	}
}

// TestPagesAdvertiseCSPReporting: the directives have to reach real pages on every
// surface, not just the middleware in isolation. A page whose CSP names no report
// endpoint reports nothing.
func TestPagesAdvertiseCSPReporting(t *testing.T) {
	t.Parallel()
	_, _, ts, env, _ := cspServer(t)

	for _, tc := range []struct{ host, path string }{
		{env.root, "/"},
		{env.auth, "/login"},
		{env.app, "/"},
	} {
		resp := do(t, ts, tc.host, tc.path)
		resp.Body.Close()
		csp := resp.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "report-uri "+web.CSPReportPath) {
			t.Errorf("%s%s: CSP has no report-uri: %q", tc.host, tc.path, csp)
		}
		// report-uri is relative, so it resolves against the document — which keeps
		// delivery same-origin on each surface without the header having to name a
		// host. See securityHeaders for why report-to is deliberately absent.
		if strings.Contains(csp, "report-to") {
			t.Errorf("%s%s: CSP advertises report-to, which makes Chrome ignore report-uri: %q",
				tc.host, tc.path, csp)
		}
	}
}

// TestAdminCSPPageDefaultsToPageViolations: extension noise dominates real CSP
// reporting, so the default view must hide it — otherwise the handful of reports that
// matter are buried.
func TestAdminCSPPageDefaultsToPageViolations(t *testing.T) {
	t.Parallel()
	st, ctx, ts, env, _ := cspServer(t)

	u, cookie := appLogin(t, ts, st, ctx, env.app, "cspadmin")
	if err := st.SetCapabilities(ctx, u.ID, true, true); err != nil {
		t.Fatalf("grant caps: %v", err)
	}

	page := store.CSPReport{
		Disposition: "enforce", Directive: "script-src-elem", BlockedURI: "inline",
		DocumentPath: "/a-real-problem", Host: testAppHost,
		Source: store.CSPReportSourcePage, Hits: 1, LastSeen: time.Now(),
	}
	ext := page
	ext.BlockedURI = "chrome-extension://noisyaddon"
	ext.DocumentPath = "/extension-noise"
	ext.Source = store.CSPReportSourceExtension
	if err := st.RecordCSPReports(ctx, []store.CSPReport{page, ext}); err != nil {
		t.Fatalf("record: %v", err)
	}

	body := getBody(t, ts, env.app, "/admin/csp", cookie)
	if !strings.Contains(body, "/a-real-problem") {
		t.Error("the default view omits a page violation")
	}
	if strings.Contains(body, "/extension-noise") {
		t.Error("the default view includes extension noise, which buries real reports")
	}

	// The extension filter shows it, and so does "all".
	if b := getBody(t, ts, env.app, "/admin/csp?source=extension", cookie); !strings.Contains(b, "/extension-noise") {
		t.Error("the extension filter doesn't show extension violations")
	}
	all := getBody(t, ts, env.app, "/admin/csp?source=all", cookie)
	if !strings.Contains(all, "/extension-noise") || !strings.Contains(all, "/a-real-problem") {
		t.Error("source=all doesn't show both classifications")
	}
}

// TestAdminCSPPageRejectsUnknownSourceFilter: a query value must never reach the WHERE
// clause as a category we don't have — an unrecognized filter falls back to the
// default rather than silently showing nothing.
func TestAdminCSPPageRejectsUnknownSourceFilter(t *testing.T) {
	t.Parallel()
	st, ctx, ts, env, _ := cspServer(t)

	u, cookie := appLogin(t, ts, st, ctx, env.app, "cspadmin2")
	if err := st.SetCapabilities(ctx, u.ID, true, true); err != nil {
		t.Fatalf("grant caps: %v", err)
	}
	if err := st.RecordCSPReports(ctx, []store.CSPReport{{
		Disposition: "enforce", Directive: "script-src", BlockedURI: "inline",
		DocumentPath: "/visible-anyway", Host: testAppHost,
		Source: store.CSPReportSourcePage, Hits: 1, LastSeen: time.Now(),
	}}); err != nil {
		t.Fatalf("record: %v", err)
	}
	body := getBody(t, ts, env.app, "/admin/csp?source=nonsense", cookie)
	if !strings.Contains(body, "/visible-anyway") {
		t.Error("an unknown source filter dropped every row instead of falling back to the default")
	}
}

// TestAdminCSPClearRequiresUserCapability: clearing deletes records, so it sits behind
// capManageUsers rather than the read-only capAny that opens the page.
func TestAdminCSPClearRequiresUserCapability(t *testing.T) {
	t.Parallel()
	st, ctx, ts, env, _ := cspServer(t)

	report := store.CSPReport{
		Disposition: "enforce", Directive: "script-src", BlockedURI: "inline",
		DocumentPath: "/x", Host: testAppHost,
		Source: store.CSPReportSourcePage, Hits: 1, LastSeen: time.Now(),
	}
	if err := st.RecordCSPReports(ctx, []store.CSPReport{report}); err != nil {
		t.Fatalf("record: %v", err)
	}

	// Catalog-only admin: may view, may not clear, and must not be shown the button.
	catalogAdmin, catalogCookie := appLogin(t, ts, st, ctx, env.app, "cspcatalog")
	if err := st.SetCapabilities(ctx, catalogAdmin.ID, false, true); err != nil {
		t.Fatalf("grant caps: %v", err)
	}
	if body := getBody(t, ts, env.app, "/admin/csp", catalogCookie); strings.Contains(body, "/admin/csp/clear") {
		t.Error("a user without manage-users was offered the clear button")
	}
	resp := post(t, ts, env.app, "/admin/csp/clear", nil, catalogCookie)
	resp.Body.Close()
	if resp.StatusCode == http.StatusSeeOther {
		t.Errorf("clear succeeded without the manage-users capability (status %d)", resp.StatusCode)
	}
	rows, _ := st.ListCSPReports(ctx, "", 10)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want the report still present", len(rows))
	}

	// A manage-users admin can clear.
	userAdmin, userCookie := appLogin(t, ts, st, ctx, env.app, "cspusers")
	if err := st.SetCapabilities(ctx, userAdmin.ID, true, false); err != nil {
		t.Fatalf("grant caps: %v", err)
	}
	if body := getBody(t, ts, env.app, "/admin/csp", userCookie); !strings.Contains(body, "/admin/csp/clear") {
		t.Error("a manage-users admin wasn't offered the clear button")
	}
	resp = post(t, ts, env.app, "/admin/csp/clear", nil, userCookie)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("clear status = %d, want 303", resp.StatusCode)
	}
	rows, _ = st.ListCSPReports(ctx, "", 10)
	if len(rows) != 0 {
		t.Errorf("rows after clear = %d, want 0", len(rows))
	}
}

// getBody fetches a page and returns its body as a string.
func getBody(t *testing.T, ts *httptest.Server, host, path string, cookies ...*http.Cookie) string {
	t.Helper()
	resp := do(t, ts, host, path, cookies...)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s%s = %d, want 200", host, path, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
