package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/store"
)

// CSPReportPath is where browsers post violation reports. Registered by every
// surface (see SharedRoutes) so it's same-origin on the app, auth and root hosts —
// a cross-origin report endpoint would need CORS preflight and would defeat the
// point of keeping violation data first-party.
//
// The leading underscore keeps it clear of the product's namespace, and it is
// deliberately not a path any page links to.
const CSPReportPath = "/_csp-report"

const (
	cspBufferSize = 512             // reports queued before new ones are dropped
	cspFlushEvery = 5 * time.Second // max latency before a batch is written
	cspFlushBatch = 100             // write early once this many are queued
	cspMaxBody    = 64 << 10        // 64 KiB: generous for a report, small for abuse
	cspSampleMax  = 200             // stored sample is truncated to this
	CSPRetention  = 90              // days a violation survives after its last hit
	cspBurst      = 20              // per-IP burst of reports...
	cspRefill     = 3 * time.Second // ...then one every this often
)

// CSPCollector receives violation reports and counts them in the background.
//
// Writes are asynchronous for the same reason the analytics recorder's are: the
// endpoint is public and a report storm must not add request latency or hold
// database connections. A full buffer drops reports rather than blocking — losing
// count of a violation is strictly better than degrading the site under a flood.
type CSPCollector struct {
	st      *store.Store
	cfg     *config.Config
	ch      chan store.CSPReport
	limiter *rateLimiter
}

// NewCSPCollector builds a collector. A nil *CSPCollector is usable: Run and Handler
// registration become no-ops, which is how surfaces built without one (tests that
// don't care) stay working.
func NewCSPCollector(st *store.Store, cfg *config.Config) *CSPCollector {
	return &CSPCollector{
		st:      st,
		cfg:     cfg,
		ch:      make(chan store.CSPReport, cspBufferSize),
		limiter: NewRateLimiter(cspBurst, cspRefill),
	}
}

// Run owns the write loop until ctx is canceled. Start it in a goroutine. Pruning
// is not done here — it's a janitor sweep (store.PruneCSPReports), alongside the
// other expiry sweeps.
func (c *CSPCollector) Run(ctx context.Context) {
	if c == nil {
		return
	}
	flush := time.NewTicker(cspFlushEvery)
	defer flush.Stop()

	var batch []store.CSPReport
	write := func(wctx context.Context) {
		if len(batch) == 0 {
			return
		}
		if err := c.st.RecordCSPReports(wctx, batch); err != nil {
			slog.Error("csp: record reports", "count", len(batch), "err", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case <-ctx.Done():
			// Drain what's queued (reports arriving while requests were draining),
			// then write on a fresh context since ctx is already canceled.
			for drained := true; drained; {
				select {
				case rep := <-c.ch:
					batch = append(batch, rep)
				default:
					drained = false
				}
			}
			fc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			write(fc)
			cancel()
			return
		case rep := <-c.ch:
			batch = append(batch, rep)
			if len(batch) >= cspFlushBatch {
				write(ctx)
			}
		case <-flush.C:
			write(ctx)
		}
	}
}

// enqueue hands a report to the writer without blocking.
func (c *CSPCollector) enqueue(rep store.CSPReport) {
	select {
	case c.ch <- rep:
	default: // buffer full — drop
	}
}

// handleReport is the endpoint. It always answers 204 with no body: reports are
// fire-and-forget, browsers ignore the response, and an error page would only
// describe our internals to anyone probing the path.
func (c *CSPCollector) handleReport(w http.ResponseWriter, r *http.Request) {
	defer w.WriteHeader(http.StatusNoContent)

	if !c.limiter.allow(clientIP(r)) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, cspMaxBody))
	if err != nil || len(body) == 0 {
		return
	}
	for _, rep := range c.normalize(parseCSPReports(body), time.Now()) {
		c.enqueue(rep)
	}
}

// cspViolation is the union of the fields we keep, from either wire format.
type cspViolation struct {
	DocumentURL string
	BlockedURL  string
	Directive   string
	Disposition string
	Sample      string
	SourceFile  string
	LineNumber  int
}

// legacyCSPReport is the `report-uri` format: a single object under a "csp-report"
// key, with hyphenated field names, posted as application/csp-report.
type legacyCSPReport struct {
	Report struct {
		DocumentURI        string `json:"document-uri"`
		BlockedURI         string `json:"blocked-uri"`
		EffectiveDirective string `json:"effective-directive"`
		ViolatedDirective  string `json:"violated-directive"`
		Disposition        string `json:"disposition"`
		ScriptSample       string `json:"script-sample"`
		SourceFile         string `json:"source-file"`
		LineNumber         int    `json:"line-number"`
	} `json:"csp-report"`
}

// reportingAPIReport is the `report-to` format: an ARRAY of envelopes, each with the
// violation under "body" and camelCase field names, posted as
// application/reports+json.
type reportingAPIReport struct {
	Type string `json:"type"`
	URL  string `json:"url"`
	Body struct {
		DocumentURL        string `json:"documentURL"`
		BlockedURL         string `json:"blockedURL"`
		EffectiveDirective string `json:"effectiveDirective"`
		Disposition        string `json:"disposition"`
		Sample             string `json:"sample"`
		SourceFile         string `json:"sourceFile"`
		LineNumber         int    `json:"lineNumber"`
	} `json:"body"`
}

// parseCSPReports decodes either wire format into a common shape.
//
// Both are needed, and shipping one is worse than it looks: Chrome prefers
// `report-to` and ignores `report-uri` when both are present, while Safari and
// Firefox implement only `report-uri`. Handling a single format therefore collects
// from a subset of browsers and reports "no violations" for the rest.
//
// The formats differ in the content type, the envelope (object vs array) AND the
// field names, so the shape is sniffed from the payload rather than trusted from
// Content-Type — some clients and proxies send application/json for both.
func parseCSPReports(body []byte) []cspViolation {
	trimmed := strings.TrimLeft(string(body), " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		var envelopes []reportingAPIReport
		if err := json.Unmarshal(body, &envelopes); err != nil {
			return nil
		}
		out := make([]cspViolation, 0, len(envelopes))
		for _, e := range envelopes {
			// A Reporting-API endpoint receives more than CSP: deprecation reports,
			// intervention reports, crashes. Only violations belong here.
			if e.Type != "" && e.Type != "csp-violation" {
				continue
			}
			doc := e.Body.DocumentURL
			if doc == "" {
				doc = e.URL // envelope URL is the fallback the spec provides
			}
			out = append(out, cspViolation{
				DocumentURL: doc,
				BlockedURL:  e.Body.BlockedURL,
				Directive:   e.Body.EffectiveDirective,
				Disposition: e.Body.Disposition,
				Sample:      e.Body.Sample,
				SourceFile:  e.Body.SourceFile,
				LineNumber:  e.Body.LineNumber,
			})
		}
		return out
	}

	var legacy legacyCSPReport
	if err := json.Unmarshal(body, &legacy); err != nil {
		return nil
	}
	rep := legacy.Report
	if rep.DocumentURI == "" && rep.BlockedURI == "" && rep.EffectiveDirective == "" {
		return nil // not a report at all
	}
	directive := rep.EffectiveDirective
	if directive == "" {
		// Older browsers send only violated-directive, which carries the whole
		// directive INCLUDING its value ("script-src 'self' https://x"). Keep the
		// name; the value is our own policy and is not worth storing per report.
		directive, _, _ = strings.Cut(rep.ViolatedDirective, " ")
	}
	return []cspViolation{{
		DocumentURL: rep.DocumentURI,
		BlockedURL:  rep.BlockedURI,
		Directive:   directive,
		Disposition: rep.Disposition,
		Sample:      rep.ScriptSample,
		SourceFile:  rep.SourceFile,
		LineNumber:  rep.LineNumber,
	}}
}

// normalize turns raw violations into storable rows, dropping the ones that aren't
// about a page we serve.
func (c *CSPCollector) normalize(vs []cspViolation, now time.Time) []store.CSPReport {
	out := make([]store.CSPReport, 0, len(vs))
	for _, v := range vs {
		doc, err := url.Parse(v.DocumentURL)
		if err != nil || doc.Host == "" {
			continue
		}
		host := HostWithoutPort(doc.Host)
		// Only accept reports about our own hosts. Anyone can POST to a public report
		// endpoint, and this is what stops unrelated junk from consuming the distinct
		// -fingerprint budget that real violations need (store.MaxDistinctCSPReports).
		// A forged report still has to name a real host of ours, and is then rate-
		// limited like any other.
		if !c.knownHost(host) {
			slog.Debug("csp: ignoring report for a host we don't serve", "host", host)
			continue
		}
		directive := strings.ToLower(strings.TrimSpace(v.Directive))
		if directive == "" {
			directive = "unknown"
		}
		out = append(out, store.CSPReport{
			Disposition: normalizeDisposition(v.Disposition),
			Directive:   directive,
			BlockedURI:  normalizeBlockedURI(v.BlockedURL),
			// Path only, then invite-token redaction: the query string carries the
			// single-use login-handoff code, so storing it would put a live
			// credential in the database and on the admin page.
			DocumentPath: RedactPath(doc.Path),
			Host:         host,
			Source:       classifyCSPSource(v.BlockedURL, v.SourceFile),
			Sample:       truncate(strings.TrimSpace(v.Sample), cspSampleMax),
			// Normalized like the blocked URI rather than stored raw: for a violation
			// on the login page the raw source-file carries the query string, and that
			// query holds a single-use auth code.
			SourceFile: normalizeBlockedURI(v.SourceFile),
			LineNumber: v.LineNumber,
			Hits:       1,
			LastSeen:   now,
		})
	}
	return out
}

// knownHost reports whether host is one of the configured surfaces. Requests to a
// host we don't recognize (the bare IP, a probe, a crawler resolving something odd)
// are not violations we can act on.
//
// WWWHost is included even though the dispatcher currently 301s everything on it, so
// no document is served there and a report naming it could only be forged. Accepting
// it costs nothing an attacker can't already do with the other three hosts, and it
// avoids a silent gap if www ever starts serving content.
func (c *CSPCollector) knownHost(host string) bool {
	if c.cfg == nil {
		return false
	}
	for _, h := range []string{c.cfg.PrimaryHost, c.cfg.AuthHost, c.cfg.RootHost, c.cfg.WWWHost} {
		if h != "" && strings.EqualFold(h, host) {
			return true
		}
	}
	return false
}

// normalizeDisposition collapses the report's disposition to the two values the spec
// defines. Unknown or missing is treated as "enforce", the conservative reading: a
// violation that actually blocked something matters more than one that didn't, so an
// unlabelled report shouldn't be filed as harmless.
func normalizeDisposition(d string) string {
	if strings.EqualFold(strings.TrimSpace(d), "report") {
		return "report"
	}
	return "enforce"
}

// normalizeBlockedURI reduces a blocked URL to scheme://host, or passes through the
// CSP keywords ("inline", "eval", "data", …) unchanged.
//
// Dropping the path is what makes aggregation work: one broken third-party embed
// hitting twelve asset paths is one problem, not twelve. It also avoids storing the
// full URLs of things loaded on our pages.
func normalizeBlockedURI(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "unknown"
	}
	// Bare keywords have no scheme separator.
	if !strings.Contains(raw, "://") {
		return strings.ToLower(raw)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// Extension URLs and opaque schemes may have no host once parsed; keep the
		// scheme, which is the part that identifies them.
		if scheme, _, ok := strings.Cut(raw, "://"); ok {
			return strings.ToLower(scheme) + "://"
		}
		return "unknown"
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// extensionSchemes are the URL schemes browser add-ons load from. Content with one of
// these came from an extension in the visitor's browser, not from anything we shipped.
//
// Stored as bare scheme names because browsers report them BOTH ways: a blocked URI
// arrives as a full "chrome-extension://<id>/inject.js", while Firefox's source-file
// is the bare "moz-extension" with no "://" and no ID at all. Firefox strips the ID
// deliberately — including it would let any site enumerate a visitor's installed
// extensions, the same fingerprinting concern that makes some blockers suppress CSP
// reports entirely.
var extensionSchemes = []string{
	"chrome-extension",
	"moz-extension",
	"safari-extension",
	"safari-web-extension",
	"ms-browser-extension",
	// Safari reports extension-injected script with this placeholder rather than a
	// real URL.
	"webkit-masked-url",
}

// hasExtensionScheme reports whether raw names an extension, accepting either a full
// URL or a bare scheme (see extensionSchemes).
func hasExtensionScheme(raw string) bool {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return false
	}
	scheme, _, ok := strings.Cut(v, "://")
	if !ok {
		scheme = v // bare scheme, as Firefox sends in source-file
	}
	for _, s := range extensionSchemes {
		if scheme == s {
			return true
		}
	}
	return false
}

// classifyCSPSource labels a violation as extension noise or a page problem, using
// both the blocked URI and the source file.
//
// source-file is what makes this work at all for the common case. An extension
// injecting an INLINE script produces blocked-uri "inline" — a bare keyword identical
// to what a genuine inline-script XSS produces — so the blocked URI alone cannot
// separate them, and this misfiled real extension noise as "page" until a captured
// report showed source-file carrying "moz-extension".
//
// Reading source-file can only ADD extension labels, never remove them from real
// violations: script in our own markup reports our document URL there, never an
// extension scheme. That direction matters, because the failure to avoid is hiding a
// genuine inline-script violation — the single most important thing this endpoint can
// report.
//
// Residual limitation: a browser that sends neither an extension-scheme blocked URI
// nor an extension-scheme source-file still lands under "page". Nothing in the report
// distinguishes it, and inventing a guess would trade a visible annoyance for a
// silent blind spot.
func classifyCSPSource(blocked, sourceFile string) string {
	if hasExtensionScheme(blocked) || hasExtensionScheme(sourceFile) {
		return store.CSPReportSourceExtension
	}
	return store.CSPReportSourcePage
}

// truncate shortens s to at most max RUNES, not bytes.
//
// Byte truncation would be wrong, not merely imprecise: cutting mid-rune yields
// invalid UTF-8, and Postgres rejects that outright ("invalid byte sequence for
// encoding UTF8") — so a violation sample containing any non-ASCII character near
// the limit would fail the whole batch insert.
func truncate(s string, max int) string {
	if len(s) <= max { // fast path: byte length bounds rune count
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
