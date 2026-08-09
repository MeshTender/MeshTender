package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/MeshTender/MeshTender/internal/config"
	"github.com/MeshTender/MeshTender/internal/store"
)

func cspTestConfig() *config.Config {
	return &config.Config{
		PrimaryHost: "app.example.test",
		AuthHost:    "auth.example.test",
		RootHost:    "example.test",
		WWWHost:     "www.example.test",
	}
}

func cspTestCollector() *CSPCollector { return NewCSPCollector(nil, cspTestConfig()) }

// drain returns everything currently queued on the collector, so a test can assert
// on what the endpoint accepted.
func (c *CSPCollector) drain() []store.CSPReport {
	var out []store.CSPReport
	for {
		select {
		case rep := <-c.ch:
			out = append(out, rep)
		default:
			return out
		}
	}
}

// TestParseCSPReportLegacyFormat covers the `report-uri` wire format: one object
// under a "csp-report" key with hyphenated field names.
func TestParseCSPReportLegacyFormat(t *testing.T) {
	t.Parallel()
	body := []byte(`{"csp-report":{
		"document-uri":"https://app.example.test/repeaters",
		"referrer":"",
		"violated-directive":"script-src 'self'",
		"effective-directive":"script-src-elem",
		"original-policy":"default-src 'self'",
		"disposition":"enforce",
		"blocked-uri":"https://evil.example.com/x.js",
		"status-code":200,
		"script-sample":""
	}}`)
	got := parseCSPReports(body)
	if len(got) != 1 {
		t.Fatalf("parsed %d violations, want 1: %+v", len(got), got)
	}
	if got[0].Directive != "script-src-elem" {
		t.Errorf("Directive = %q, want script-src-elem", got[0].Directive)
	}
	if got[0].BlockedURL != "https://evil.example.com/x.js" {
		t.Errorf("BlockedURL = %q", got[0].BlockedURL)
	}
	if got[0].DocumentURL != "https://app.example.test/repeaters" {
		t.Errorf("DocumentURL = %q", got[0].DocumentURL)
	}
}

// TestParseCSPReportReportingAPIFormat covers the modern `report-to` format, which
// differs from the legacy one in all three ways that matter: it's an ARRAY, the
// violation sits under "body", and the field names are camelCase.
//
// This is the regression that matters most. Handling only the legacy shape would
// silently discard every report from Chrome — which prefers report-to and ignores
// report-uri when both are advertised — leaving the admin page reading "no
// violations" for the majority of visitors.
func TestParseCSPReportReportingAPIFormat(t *testing.T) {
	t.Parallel()
	body := []byte(`[{
		"age":12,
		"type":"csp-violation",
		"url":"https://app.example.test/repeaters",
		"user_agent":"Mozilla/5.0",
		"body":{
			"documentURL":"https://app.example.test/repeaters",
			"referrer":"",
			"blockedURL":"https://evil.example.com/x.js",
			"effectiveDirective":"script-src-elem",
			"originalPolicy":"default-src 'self'",
			"disposition":"enforce",
			"statusCode":200,
			"sample":"",
			"sourceFile":"https://app.example.test/repeaters",
			"lineNumber":42,
			"columnNumber":7
		}
	}]`)
	got := parseCSPReports(body)
	if len(got) != 1 {
		t.Fatalf("parsed %d violations, want 1: %+v", len(got), got)
	}
	if got[0].Directive != "script-src-elem" {
		t.Errorf("Directive = %q, want script-src-elem", got[0].Directive)
	}
	if got[0].BlockedURL != "https://evil.example.com/x.js" {
		t.Errorf("BlockedURL = %q", got[0].BlockedURL)
	}
	if got[0].DocumentURL != "https://app.example.test/repeaters" {
		t.Errorf("DocumentURL = %q", got[0].DocumentURL)
	}
}

// TestParseCSPReportsHandlesBatchedAndNonViolationReports: a Reporting-API endpoint
// receives more than CSP violations (deprecations, interventions, crashes), and
// several reports arrive in one POST. Only violations may be counted.
func TestParseCSPReportsHandlesBatchedAndNonViolationReports(t *testing.T) {
	t.Parallel()
	body := []byte(`[
		{"type":"deprecation","url":"https://app.example.test/","body":{"id":"x"}},
		{"type":"csp-violation","url":"https://app.example.test/a","body":{"documentURL":"https://app.example.test/a","blockedURL":"inline","effectiveDirective":"script-src-elem"}},
		{"type":"csp-violation","url":"https://app.example.test/b","body":{"documentURL":"https://app.example.test/b","blockedURL":"eval","effectiveDirective":"script-src"}}
	]`)
	got := parseCSPReports(body)
	if len(got) != 2 {
		t.Fatalf("parsed %d violations, want 2 (the deprecation report must be ignored): %+v", len(got), got)
	}
}

// TestParseCSPReportFallsBackToViolatedDirective: older browsers send only
// violated-directive, which includes the directive's VALUE. Keep the name, drop the
// value — it's our own policy, not information about the violation.
func TestParseCSPReportFallsBackToViolatedDirective(t *testing.T) {
	t.Parallel()
	body := []byte(`{"csp-report":{"document-uri":"https://app.example.test/",
		"violated-directive":"style-src 'self' 'unsafe-inline'","blocked-uri":"inline"}}`)
	got := parseCSPReports(body)
	if len(got) != 1 || got[0].Directive != "style-src" {
		t.Fatalf("Directive = %+v, want style-src", got)
	}
}

func TestParseCSPReportsRejectsJunk(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		``, `not json`, `{}`, `[]`, `{"csp-report":{}}`, `[{"type":"csp-violation"}]`, `null`,
	} {
		if got := parseCSPReports([]byte(body)); len(got) > 0 {
			// A bodiless reporting envelope parses to an empty violation, which
			// normalize() then drops for having no document URL — so anything that
			// slips through here must still not reach the store.
			if body == `[{"type":"csp-violation"}]` {
				if reps := cspTestCollector().normalize(got, time.Now()); len(reps) > 0 {
					t.Errorf("body %q produced a storable report: %+v", body, reps)
				}
				continue
			}
			t.Errorf("parseCSPReports(%q) = %+v, want nothing", body, got)
		}
	}
}

// TestNormalizeDropsQueryStringAndInviteToken is a security regression test.
//
// The login handoff carries a single-use auth code in the query string
// (/login?next=…&state=<code>) and an invite token in the path. A violation on
// either page must not write those into a table that an admin screen renders — the
// report would otherwise hand a live credential to anyone who can read the page, and
// keep it for the retention window.
func TestNormalizeDropsQueryStringAndInviteToken(t *testing.T) {
	t.Parallel()
	c := cspTestCollector()
	got := c.normalize([]cspViolation{
		{DocumentURL: "https://auth.example.test/login?next=%2F&state=SUPERSECRETAUTHCODE", BlockedURL: "inline", Directive: "script-src-elem"},
		{DocumentURL: "https://app.example.test/invite/SUPERSECRETINVITETOKEN", BlockedURL: "inline", Directive: "script-src-elem"},
	}, time.Now())
	if len(got) != 2 {
		t.Fatalf("normalized %d reports, want 2", len(got))
	}
	if got[0].DocumentPath != "/login" {
		t.Errorf("DocumentPath = %q, want /login (query string must be dropped)", got[0].DocumentPath)
	}
	if got[1].DocumentPath != "/invite/:token" {
		t.Errorf("DocumentPath = %q, want /invite/:token", got[1].DocumentPath)
	}
	for _, r := range got {
		for _, secret := range []string{"SUPERSECRETAUTHCODE", "SUPERSECRETINVITETOKEN"} {
			if strings.Contains(r.DocumentPath+r.Sample+r.BlockedURI+r.Host, secret) {
				t.Errorf("a secret survived normalization into %+v", r)
			}
		}
	}
}

// TestNormalizeRejectsForeignDocumentHosts: the endpoint is public and
// unauthenticated, so anything can post to it. Reports about pages we don't serve
// are dropped — that's what keeps junk from consuming the distinct-fingerprint
// budget real violations need (store.MaxDistinctCSPReports).
func TestNormalizeRejectsForeignDocumentHosts(t *testing.T) {
	t.Parallel()
	c := cspTestCollector()
	for _, doc := range []string{
		"https://attacker.example.com/x", // not ours
		"about:blank",                    // no host
		"",                               // unparseable
		"chrome-extension://abcdef/page.html",
	} {
		if got := c.normalize([]cspViolation{{DocumentURL: doc, BlockedURL: "inline", Directive: "script-src"}}, time.Now()); len(got) != 0 {
			t.Errorf("document %q was accepted: %+v", doc, got)
		}
	}
	// Every configured surface is accepted, including www.
	for _, doc := range []string{
		"https://app.example.test/x", "https://auth.example.test/x",
		"https://example.test/x", "https://www.example.test/x",
		"http://app.example.test:8090/x", // a port must not defeat the match
	} {
		if got := c.normalize([]cspViolation{{DocumentURL: doc, BlockedURL: "inline", Directive: "script-src"}}, time.Now()); len(got) != 1 {
			t.Errorf("document %q was rejected", doc)
		}
	}
}

func TestNormalizeBlockedURI(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"inline", "inline"},
		{"eval", "eval"},
		{"INLINE", "inline"},
		{"data", "data"},
		{"", "unknown"},
		// Path and query are dropped: one broken embed across a dozen asset URLs is
		// one problem, not a dozen rows.
		{"https://cdn.example.com/a/b/c.js?v=2", "https://cdn.example.com"},
		{"https://CDN.example.com/x", "https://cdn.example.com"},
		{"chrome-extension://abcdefghijklmnop/inject.js", "chrome-extension://abcdefghijklmnop"},
		{"data:image/png;base64,AAAA", "data:image/png;base64,aaaa"}, // no "://", treated as a keyword
	}
	for _, c := range cases {
		if got := normalizeBlockedURI(c.in); got != c.want {
			t.Errorf("normalizeBlockedURI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClassifyCSPSource(t *testing.T) {
	t.Parallel()
	// An extension scheme in EITHER field is enough.
	extension := []string{
		"chrome-extension://abc/inject.js",
		"moz-extension://abc/inject.js",
		"safari-web-extension://abc/inject.js",
		"webkit-masked-url://hidden/",
		"CHROME-EXTENSION://abc/x.js",
		// Bare schemes, the form Firefox uses for source-file.
		"moz-extension",
		"chrome-extension",
		" MOZ-EXTENSION ",
	}
	for _, v := range extension {
		if got := classifyCSPSource(v, ""); got != store.CSPReportSourceExtension {
			t.Errorf("classifyCSPSource(%q, \"\") = %q, want extension", v, got)
		}
		if got := classifyCSPSource("inline", v); got != store.CSPReportSourceExtension {
			t.Errorf("classifyCSPSource(\"inline\", %q) = %q, want extension", v, got)
		}
	}
	for _, v := range []string{"inline", "eval", "https://cdn.example.com/x.js", "", "https", "extension"} {
		if got := classifyCSPSource(v, ""); got != store.CSPReportSourcePage {
			t.Errorf("classifyCSPSource(%q, \"\") = %q, want page", v, got)
		}
	}
	// A page's own script must never be mislabelled as extension noise — that would
	// hide a real inline-script violation, the one outcome worth avoiding.
	if got := classifyCSPSource("inline", "https://app.example.test/repeaters"); got != store.CSPReportSourcePage {
		t.Errorf("an inline violation sourced from our own document = %q, want page", got)
	}
}

// TestFirefoxExtensionInjectedInlineScriptIsClassified is built from a REAL captured
// report, byte for byte, rather than my guess at one — a Firefox 153 violation caused
// by an extension injecting an inline script into an app page.
//
// It's the case the classifier originally got wrong: blocked-uri is the bare keyword
// "inline", identical to a genuine inline-script XSS, and the only thing separating
// them is source-file. Note the shape of that field — "moz-extension", with no "://"
// and no extension ID, because Firefox strips the ID to prevent sites enumerating
// installed extensions. Matching on "moz-extension://" (the obvious implementation)
// misses it entirely.
//
// Also captured here: Firefox sends no script-sample at all.
func TestFirefoxExtensionInjectedInlineScriptIsClassified(t *testing.T) {
	t.Parallel()
	body := []byte(`{"csp-report":{"blocked-uri":"inline","column-number":20,` +
		`"disposition":"enforce","document-uri":"https://app.example.test/admin/csp",` +
		`"effective-directive":"script-src-elem","line-number":4941,` +
		`"original-policy":"default-src 'self'; script-src 'self' 'nonce-AXtuqT5oPZXniijL/OGihA'",` +
		`"referrer":"","source-file":"moz-extension","status-code":200,` +
		`"violated-directive":"script-src-elem"}}`)

	got := cspTestCollector().normalize(parseCSPReports(body), time.Now())
	if len(got) != 1 {
		t.Fatalf("normalized %d reports, want 1: %+v", len(got), got)
	}
	r := got[0]
	if r.Source != store.CSPReportSourceExtension {
		t.Errorf("Source = %q, want extension — this is extension noise filed as a page problem", r.Source)
	}
	if r.SourceFile != "moz-extension" {
		t.Errorf("SourceFile = %q, want moz-extension", r.SourceFile)
	}
	if r.LineNumber != 4941 {
		t.Errorf("LineNumber = %d, want 4941", r.LineNumber)
	}
	if r.Directive != "script-src-elem" || r.BlockedURI != "inline" {
		t.Errorf("Directive/BlockedURI = %q/%q", r.Directive, r.BlockedURI)
	}
	if r.DocumentPath != "/admin/csp" {
		t.Errorf("DocumentPath = %q", r.DocumentPath)
	}
	if r.Sample != "" {
		t.Errorf("Sample = %q; Firefox sends none, so anything here is invented", r.Sample)
	}
}

// TestNormalizeStripsQueryFromSourceFile: source-file is a URL like any other, so it
// carries the same auth-code hazard as document-uri. A violation on the login page
// must not preserve the handoff code just because it arrived in a different field.
func TestNormalizeStripsQueryFromSourceFile(t *testing.T) {
	t.Parallel()
	got := cspTestCollector().normalize([]cspViolation{{
		DocumentURL: "https://auth.example.test/login",
		BlockedURL:  "inline",
		Directive:   "script-src-elem",
		SourceFile:  "https://auth.example.test/login?next=%2F&state=SUPERSECRETAUTHCODE",
		LineNumber:  12,
	}}, time.Now())
	if len(got) != 1 {
		t.Fatalf("normalized %d reports, want 1", len(got))
	}
	if strings.Contains(got[0].SourceFile, "SUPERSECRETAUTHCODE") {
		t.Errorf("an auth code survived into SourceFile: %q", got[0].SourceFile)
	}
	if got[0].SourceFile != "https://auth.example.test" {
		t.Errorf("SourceFile = %q, want scheme://host", got[0].SourceFile)
	}
}

// TestInlineViolationWithNoSourceHintCountsAsPage pins the residual limitation, now
// that source-file narrows it (see TestFirefoxExtensionInjectedInlineScriptIsClassified).
//
// When a report gives blocked-uri "inline" and NO extension-scheme source-file, there
// is nothing in it that distinguishes extension noise from a genuine inline-script
// violation — so it stays "page". That's the safe direction: guessing "extension"
// would hide the single most important thing this endpoint can report. During
// development an extension doing exactly this was mistaken for an app bug; the fix is
// that the report exists to be checked, not that it's filed as harmless.
func TestInlineViolationWithNoSourceHintCountsAsPage(t *testing.T) {
	t.Parallel()
	c := cspTestCollector()
	got := c.normalize([]cspViolation{{
		DocumentURL: "https://app.example.test/repeaters",
		BlockedURL:  "inline",
		Directive:   "script-src-elem",
		Sample:      "window.__extensionThing=1",
	}}, time.Now())
	if len(got) != 1 || got[0].Source != store.CSPReportSourcePage {
		t.Fatalf("normalized = %+v, want one report classified as page", got)
	}
}

func TestNormalizeDefaultsAndTruncation(t *testing.T) {
	t.Parallel()
	c := cspTestCollector()
	got := c.normalize([]cspViolation{{
		DocumentURL: "https://app.example.test/x",
		BlockedURL:  "inline",
		Directive:   "", // missing
		Disposition: "", // missing
		Sample:      strings.Repeat("a", cspSampleMax*2),
	}}, time.Now())
	if len(got) != 1 {
		t.Fatalf("normalized %d reports, want 1", len(got))
	}
	if got[0].Directive != "unknown" {
		t.Errorf("Directive = %q, want unknown", got[0].Directive)
	}
	// An unlabelled disposition is read as "enforce": a violation that actually
	// blocked something matters more than one that didn't, so the conservative
	// default must not file it as harmless report-only noise.
	if got[0].Disposition != "enforce" {
		t.Errorf("Disposition = %q, want enforce", got[0].Disposition)
	}
	if len([]rune(got[0].Sample)) > cspSampleMax+1 {
		t.Errorf("sample not truncated: %d runes", len([]rune(got[0].Sample)))
	}
}

// TestTruncateKeepsValidUTF8: truncating mid-rune produces invalid UTF-8, which
// Postgres rejects outright — one violation sample with a multi-byte character near
// the limit would fail the entire batch insert, losing every report in it.
func TestTruncateKeepsValidUTF8(t *testing.T) {
	t.Parallel()
	// "…" is 3 bytes, so a run of them crosses the byte limit mid-rune repeatedly.
	got := truncate(strings.Repeat("…", cspSampleMax), cspSampleMax/2)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated sample is not valid UTF-8: %q", got)
	}
	if n := len([]rune(got)); n > cspSampleMax/2+1 {
		t.Errorf("truncated to %d runes, want at most %d", n, cspSampleMax/2+1)
	}
	if short := "плохо"; truncate(short, cspSampleMax) != short {
		t.Errorf("a short multi-byte string was altered: %q", truncate(short, cspSampleMax))
	}
}

func TestNormalizeKeepsReportOnlyDisposition(t *testing.T) {
	t.Parallel()
	c := cspTestCollector()
	got := c.normalize([]cspViolation{{
		DocumentURL: "https://app.example.test/x", BlockedURL: "inline",
		Directive: "style-src", Disposition: "report",
	}}, time.Now())
	if len(got) != 1 || got[0].Disposition != "report" {
		t.Fatalf("normalized = %+v, want disposition report", got)
	}
}

// TestCSPReportEndpointAcceptsBothFormats drives the real handler.
func TestCSPReportEndpointAcceptsBothFormats(t *testing.T) {
	t.Parallel()
	legacy := `{"csp-report":{"document-uri":"https://app.example.test/a","blocked-uri":"inline","effective-directive":"script-src-elem","disposition":"enforce"}}`
	modern := `[{"type":"csp-violation","url":"https://app.example.test/b","body":{"documentURL":"https://app.example.test/b","blockedURL":"eval","effectiveDirective":"script-src","disposition":"enforce"}}]`

	for _, tc := range []struct{ name, ct, body, wantPath string }{
		{"report-uri", "application/csp-report", legacy, "/a"},
		{"reporting-api", "application/reports+json", modern, "/b"},
		// Some clients and proxies relabel the body; the shape is sniffed, so the
		// content type must not decide whether a report is understood.
		{"mislabelled-content-type", "application/json", modern, "/b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := cspTestCollector()
			req := httptest.NewRequest(http.MethodPost, CSPReportPath, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.ct)
			rec := httptest.NewRecorder()
			c.handleReport(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Errorf("status = %d, want 204", rec.Code)
			}
			got := c.drain()
			if len(got) != 1 {
				t.Fatalf("enqueued %d reports, want 1", len(got))
			}
			if got[0].DocumentPath != tc.wantPath {
				t.Errorf("DocumentPath = %q, want %q", got[0].DocumentPath, tc.wantPath)
			}
		})
	}
}

// TestCSPReportEndpointAlwaysAnswers204: reports are fire-and-forget. Browsers
// ignore the response, so junk gets the same silent 204 as a good report rather than
// an error page describing our internals to whoever is probing the path.
func TestCSPReportEndpointAlwaysAnswers204(t *testing.T) {
	t.Parallel()
	c := cspTestCollector()
	for _, body := range []string{"", "garbage", "{}", `{"csp-report":{"document-uri":"https://elsewhere.example/x"}}`} {
		rec := httptest.NewRecorder()
		c.handleReport(rec, httptest.NewRequest(http.MethodPost, CSPReportPath, strings.NewReader(body)))
		if rec.Code != http.StatusNoContent {
			t.Errorf("body %q: status = %d, want 204", body, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("body %q: response body = %q, want empty", body, rec.Body.String())
		}
	}
	if got := c.drain(); len(got) != 0 {
		t.Errorf("junk was accepted for storage: %+v", got)
	}
}

// TestCSPReportEndpointRateLimits: the endpoint is unauthenticated, so a single
// client must not be able to drive unbounded work.
func TestCSPReportEndpointRateLimits(t *testing.T) {
	t.Parallel()
	c := cspTestCollector()
	body := `{"csp-report":{"document-uri":"https://app.example.test/a","blocked-uri":"inline","effective-directive":"script-src"}}`
	for i := 0; i < cspBurst*3; i++ {
		req := httptest.NewRequest(http.MethodPost, CSPReportPath, strings.NewReader(body))
		req.RemoteAddr = "203.0.113.7:5555"
		rec := httptest.NewRecorder()
		c.handleReport(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: status = %d, want 204 even when limited", i, rec.Code)
		}
	}
	accepted := len(c.drain())
	if accepted > cspBurst {
		t.Errorf("accepted %d reports from one IP, want at most the %d burst", accepted, cspBurst)
	}
	if accepted == 0 {
		t.Error("accepted nothing at all — the limiter is rejecting the first request")
	}
}

// TestCSPReportEndpointDropsOversizeBody: the body cap truncates rather than
// buffering unbounded data, and truncated JSON must not parse into a report.
func TestCSPReportEndpointDropsOversizeBody(t *testing.T) {
	t.Parallel()
	c := cspTestCollector()
	huge := `{"csp-report":{"document-uri":"https://app.example.test/a","blocked-uri":"inline",` +
		`"effective-directive":"script-src","script-sample":"` + strings.Repeat("x", cspMaxBody*2) + `"}}`
	rec := httptest.NewRecorder()
	c.handleReport(rec, httptest.NewRequest(http.MethodPost, CSPReportPath, strings.NewReader(huge)))
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if got := c.drain(); len(got) != 0 {
		t.Errorf("an oversize body was accepted: %+v", got)
	}
}

// TestSecurityHeadersAdvertiseReportURIOnly pins a decision that is easy to
// "modernize" into a silent outage.
//
// report-to and report-uri do not compose: a browser supporting report-to ignores
// report-uri entirely when both are present. So adding report-to doesn't widen
// coverage — it moves Chrome onto that path exclusively. The browser test
// (TestCSPViolationIsReportedByARealBrowser) shows report-uri delivering a real
// violation within a second, and report-to delivering nothing measurable in the same
// environment. Until report-to can be verified against a trusted certificate,
// advertising it would trade a proven path for an unproven one.
func TestSecurityHeadersAdvertiseReportURIOnly(t *testing.T) {
	t.Parallel()
	env := &Env{Cfg: cspTestConfig(), csp: cspTestCollector()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://app.example.test/x", nil)
	req.Host = "app.example.test"
	env.securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "report-uri "+CSPReportPath) {
		t.Errorf("CSP lacks report-uri: %q", csp)
	}
	if strings.Contains(csp, "report-to") {
		t.Errorf("CSP advertises report-to, which makes Chrome ignore the report-uri "+
			"that is the only mechanism verified to deliver: %q", csp)
	}
	if got := rec.Header().Get("Reporting-Endpoints"); got != "" {
		t.Errorf("Reporting-Endpoints = %q; it only has meaning alongside report-to", got)
	}
}

// TestSecurityHeadersOmitReportingWithoutCollector: with reporting off, no browser
// should be told to post to a path that isn't registered.
func TestSecurityHeadersOmitReportingWithoutCollector(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	(&Env{}).securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if csp := rec.Header().Get("Content-Security-Policy"); strings.Contains(csp, "report") {
		t.Errorf("CSP advertises reporting with no collector: %q", csp)
	}
	if got := rec.Header().Get("Reporting-Endpoints"); got != "" {
		t.Errorf("Reporting-Endpoints = %q, want empty", got)
	}
}

// TestCrossSiteWriteBlockerExemptsReportPath: violation reports are POSTs the
// browser generates out-of-band from the document, and no specification pins down
// the Sec-Fetch-Site value on that delivery. If the CSRF layer rejected them,
// reporting would fail silently and look merely quiet — the worst failure mode for a
// monitoring feature.
func TestCrossSiteWriteBlockerExemptsReportPath(t *testing.T) {
	t.Parallel()
	h := blockCrossSiteWrites(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, CSPReportPath, strings.NewReader("{}"))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("report POST labeled cross-site got %d, want it to pass through", rec.Code)
	}

	// The exemption must be exactly that one path — everything else still blocks.
	req = httptest.NewRequest(http.MethodPost, "/repeaters", strings.NewReader("x"))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST to a normal path got %d, want 403", rec.Code)
	}
}
