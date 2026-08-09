//go:build browser

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/store"
)

// flushCSP drains whatever the collector has queued into the database, the same way
// shutdown does (cancelling its context makes Run write what's buffered). Reports
// arrive asynchronously from the browser, so this is called after waiting for one.
func (e *e2eServer) flushCSP(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.srv.CollectCSPReports(ctx)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the CSP collector didn't finish draining")
	}
}

// waitForCSPReport polls until at least one violation has been stored, flushing the
// collector on each pass. Returns nil if the deadline passes with nothing recorded.
//
// Polling is necessary rather than sloppy: a browser does not deliver a violation
// report during the navigation that triggered it. The Reporting API explicitly
// queues reports and delivers them out-of-band, batched on its own timer, so the only
// honest way to observe delivery is to wait for it.
func (e *e2eServer) waitForCSPReport(t *testing.T, within time.Duration) []store.CSPReportRow {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		e.flushCSP(t)
		rows, err := e.store.ListCSPReports(e.ctx, "", 10)
		if err != nil {
			t.Fatalf("list csp reports: %v", err)
		}
		if len(rows) > 0 {
			return rows
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestCSPViolationIsReportedByARealBrowser is the end-to-end proof that the reporting
// pipeline works against an actual browser, which is the only place it can be proven.
//
// Everything before this point is our own reading of the specifications: which header
// a browser honours, which wire format it posts, what it puts in the fields. A Go test
// that posts a hand-written body only proves we can parse what we think a browser
// sends. This triggers a genuine violation in Chrome and checks the row that lands.
//
// It deliberately does NOT call watch.assertClean — the violation is the point.
func TestCSPViolationIsReportedByARealBrowser(t *testing.T) {
	srv := newE2EServer(t)
	_, cookie := srv.login(t, "e2ecspreport")

	bctx, cancel, _ := startBrowser(t)
	defer cancel()

	// img-src is the cleanest directive to violate on purpose: the policy allows
	// 'self', data: and the CARTO tile host, so a foreign image host is blocked
	// before any request leaves the browser. No network dependency, and it exercises
	// the blocked-URI normalization (a full URL must be stored as scheme://host).
	const violate = `(() => {
	  const img = document.createElement("img");
	  img.src = "https://csp-violation.invalid/pixel.png";
	  document.body.appendChild(img);
	  return true;
	})()`

	var ok bool
	err := chromedp.Run(bctx,
		network.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(srv.appURL+"/repeaters"),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		chromedp.Evaluate(violate, &ok),
		// Give the browser a moment to notice the violation before we start polling.
		chromedp.Sleep(time.Second),
	)
	if err != nil {
		t.Fatalf("drive browser: %v", err)
	}
	if !ok {
		t.Fatal("the violating element was never inserted")
	}

	rows := srv.waitForCSPReport(t, 45*time.Second)
	if len(rows) == 0 {
		t.Fatal("no violation report arrived from the browser within 45s — the reporting " +
			"headers, the endpoint, or the write path is broken")
	}

	var found bool
	for _, r := range rows {
		if r.BlockedURI != "https://csp-violation.invalid" {
			continue
		}
		found = true
		if r.DocumentPath != "/repeaters" {
			t.Errorf("DocumentPath = %q, want /repeaters", r.DocumentPath)
		}
		if r.Host != appHost() {
			t.Errorf("Host = %q, want %q", r.Host, appHost())
		}
		if r.Directive != "img-src" {
			t.Errorf("Directive = %q, want img-src", r.Directive)
		}
		if r.Disposition != "enforce" {
			t.Errorf("Disposition = %q, want enforce", r.Disposition)
		}
		if r.Source != store.CSPReportSourcePage {
			t.Errorf("Source = %q, want page", r.Source)
		}
		if r.Hits < 1 {
			t.Errorf("Hits = %d", r.Hits)
		}
	}
	if !found {
		t.Errorf("the stored reports don't include the violation we caused: %+v", rows)
	}
}
