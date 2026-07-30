package store

import (
	"fmt"
	"testing"
	"time"
)

func cspReport(directive, blocked, path string) CSPReport {
	return CSPReport{
		Disposition:  "enforce",
		Directive:    directive,
		BlockedURI:   blocked,
		DocumentPath: path,
		Host:         "app.example.test",
		Source:       CSPReportSourcePage,
		Hits:         1,
		LastSeen:     time.Now(),
	}
}

// TestCSPReportsAggregateByFingerprint is the core of the design: repeated reports of
// the same violation must become one row with a counter, not N rows. A row-per-report
// table would be swamped by a single misbehaving extension.
func TestCSPReportsAggregateByFingerprint(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	rep := cspReport("script-src-elem", "inline", "/repeaters")
	// Two separate calls, each with duplicates inside the batch: both the in-batch
	// fold and the ON CONFLICT path have to count.
	if err := st.RecordCSPReports(ctx, []CSPReport{rep, rep, rep}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := st.RecordCSPReports(ctx, []CSPReport{rep, rep}); err != nil {
		t.Fatalf("record again: %v", err)
	}

	rows, err := st.ListCSPReports(ctx, "", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("stored %d rows, want 1 aggregated row: %+v", len(rows), rows)
	}
	if rows[0].Hits != 5 {
		t.Errorf("Hits = %d, want 5", rows[0].Hits)
	}
}

// TestCSPReportsSeparateDistinctViolations: aggregation must not over-merge. Each
// field in the fingerprint identifies a different problem.
func TestCSPReportsSeparateDistinctViolations(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	base := cspReport("script-src-elem", "inline", "/repeaters")
	differs := []CSPReport{
		base,
		func() CSPReport { r := base; r.Directive = "style-src"; return r }(),
		func() CSPReport { r := base; r.BlockedURI = "https://cdn.example.com"; return r }(),
		func() CSPReport { r := base; r.DocumentPath = "/orgs"; return r }(),
		func() CSPReport { r := base; r.Host = "auth.example.test"; return r }(),
		// Enforce vs report-only is the whole point of a report-only trial policy,
		// so it must never collapse into the enforced row.
		func() CSPReport { r := base; r.Disposition = "report"; return r }(),
	}
	if err := st.RecordCSPReports(ctx, differs); err != nil {
		t.Fatalf("record: %v", err)
	}
	rows, err := st.ListCSPReports(ctx, "", 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != len(differs) {
		t.Fatalf("stored %d rows, want %d — distinct violations were merged", len(rows), len(differs))
	}
}

// TestCSPReportFingerprintIgnoresSample: the sample varies between otherwise
// identical violations, so including it would shatter one problem into many rows —
// the row-per-report behaviour the aggregate table exists to prevent.
func TestCSPReportFingerprintIgnoresSample(t *testing.T) {
	t.Parallel()
	a := cspReport("script-src-elem", "inline", "/x")
	a.Sample = "one snippet"
	b := cspReport("script-src-elem", "inline", "/x")
	b.Sample = "a completely different snippet"
	if a.Fingerprint() != b.Fingerprint() {
		t.Error("samples changed the fingerprint; identical violations would store as separate rows")
	}

	// And the length-prefixed hash must not let field boundaries slide: these two
	// differ only in where one value ends and the next begins.
	x := cspReport("script", "-srcinline", "/x")
	y := cspReport("script-src", "inline", "/x")
	if x.Fingerprint() == y.Fingerprint() {
		t.Error("field boundaries collide in the fingerprint")
	}
}

// TestCSPReportsStoreSourceFileAndLine: for a violation in our own markup these two
// are the difference between "something inline was blocked somewhere" and a place to
// look, so they have to survive the round trip.
func TestCSPReportsStoreSourceFileAndLine(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	rep := cspReport("script-src-elem", "inline", "/admin/csp")
	rep.Source = CSPReportSourceExtension
	rep.SourceFile = "moz-extension"
	rep.LineNumber = 4941
	if err := st.RecordCSPReports(ctx, []CSPReport{rep, rep}); err != nil {
		t.Fatalf("record: %v", err)
	}
	rows, err := st.ListCSPReports(ctx, "", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("stored %d rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].SourceFile != "moz-extension" || rows[0].LineNumber != 4941 {
		t.Errorf("SourceFile/LineNumber = %q/%d, want moz-extension/4941",
			rows[0].SourceFile, rows[0].LineNumber)
	}
	if rows[0].Hits != 2 {
		t.Errorf("Hits = %d, want 2", rows[0].Hits)
	}
}

// TestCSPReportFingerprintIgnoresLineNumber: template edits move line numbers, so
// including one would split a single ongoing violation into a fresh row per deploy —
// the counter would reset and the history would fragment.
func TestCSPReportFingerprintIgnoresLineNumber(t *testing.T) {
	t.Parallel()
	a := cspReport("script-src-elem", "inline", "/x")
	a.Source = CSPReportSourceExtension
	a.SourceFile = "moz-extension"
	a.LineNumber = 4941
	b := a
	b.LineNumber = 5102 // same problem after an unrelated template change
	if a.Fingerprint() != b.Fingerprint() {
		t.Error("the line number changed the fingerprint; one violation would become a new row per deploy")
	}
	// The classification it feeds, though, must still separate rows.
	c := a
	c.Source = CSPReportSourcePage
	c.SourceFile = ""
	if a.Fingerprint() == c.Fingerprint() {
		t.Error("extension and page violations on the same path share a fingerprint")
	}
}

// TestCSPReportsKeepFirstSample: the stored sample is a debugging hint, and a stable
// one is more useful than one that churns with every hit.
func TestCSPReportsKeepFirstSample(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	first := cspReport("script-src-elem", "inline", "/x")
	first.Sample = "the original snippet"
	later := first
	later.Sample = "a later snippet"
	if err := st.RecordCSPReports(ctx, []CSPReport{first}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := st.RecordCSPReports(ctx, []CSPReport{later}); err != nil {
		t.Fatalf("record: %v", err)
	}
	rows, err := st.ListCSPReports(ctx, "", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].Sample != "the original snippet" {
		t.Fatalf("rows = %+v, want the first sample retained", rows)
	}
}

// TestCSPReportsFillMissingSample: if the first report of a violation had no sample,
// a later one that does should fill it in — an empty sample is nothing to protect.
func TestCSPReportsFillMissingSample(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	bare := cspReport("script-src-elem", "inline", "/x")
	withSample := bare
	withSample.Sample = "eventually useful"
	if err := st.RecordCSPReports(ctx, []CSPReport{bare}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := st.RecordCSPReports(ctx, []CSPReport{withSample}); err != nil {
		t.Fatalf("record: %v", err)
	}
	rows, _ := st.ListCSPReports(ctx, "", 10)
	if len(rows) != 1 || rows[0].Sample != "eventually useful" {
		t.Fatalf("rows = %+v, want the sample filled in", rows)
	}
}

// TestCSPReportsCapDistinctFingerprints is the abuse guard. The endpoint is public,
// so without a cap an attacker varying one field turns an upsert into an unbounded
// insert. The cap must stop NEW fingerprints while still counting hits on violations
// already recorded — a naive `WHERE count < cap` would silently freeze the counters
// on every real violation the moment the table filled.
func TestCSPReportsCapDistinctFingerprints(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	known := cspReport("script-src-elem", "inline", "/known")
	if err := st.RecordCSPReports(ctx, []CSPReport{known}); err != nil {
		t.Fatalf("record known: %v", err)
	}

	// Flood well past the cap with distinct fingerprints.
	flood := make([]CSPReport, 0, MaxDistinctCSPReports+50)
	for i := 0; i < MaxDistinctCSPReports+50; i++ {
		flood = append(flood, cspReport("script-src-elem", "inline", fmt.Sprintf("/flood/%d", i)))
	}
	if err := st.RecordCSPReports(ctx, flood); err != nil {
		t.Fatalf("record flood: %v", err)
	}

	stats, err := st.CSPReportStats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Distinct > MaxDistinctCSPReports {
		t.Errorf("stored %d distinct rows, want at most %d", stats.Distinct, MaxDistinctCSPReports)
	}
	if !stats.AtCapacity {
		t.Error("AtCapacity is false with a full table — the admin page wouldn't warn that reports are being dropped")
	}

	// The pre-existing violation must keep counting despite the full table.
	if err := st.RecordCSPReports(ctx, []CSPReport{known, known}); err != nil {
		t.Fatalf("record known again: %v", err)
	}
	rows, err := st.ListCSPReports(ctx, "", MaxDistinctCSPReports)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, r := range rows {
		if r.DocumentPath == "/known" {
			found = true
			if r.Hits != 3 {
				t.Errorf("known violation Hits = %d, want 3 — hits stopped counting once the table filled", r.Hits)
			}
		}
	}
	if !found {
		t.Fatal("the known violation is missing from the table")
	}
}

func TestCSPReportsFilterAndStatsBySource(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	page := cspReport("script-src-elem", "inline", "/x")
	ext := cspReport("script-src-elem", "chrome-extension://abc", "/x")
	ext.Source = CSPReportSourceExtension
	if err := st.RecordCSPReports(ctx, []CSPReport{page, ext, ext}); err != nil {
		t.Fatalf("record: %v", err)
	}

	pageRows, err := st.ListCSPReports(ctx, CSPReportSourcePage, 10)
	if err != nil {
		t.Fatalf("list page: %v", err)
	}
	if len(pageRows) != 1 || pageRows[0].Source != CSPReportSourcePage {
		t.Fatalf("page rows = %+v, want exactly the one page violation", pageRows)
	}
	all, err := st.ListCSPReports(ctx, "", 10)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all rows = %d, want 2", len(all))
	}

	stats, err := st.CSPReportStats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.PageDistinct != 1 || stats.ExtensionDistinct != 1 {
		t.Errorf("stats page/extension = %d/%d, want 1/1", stats.PageDistinct, stats.ExtensionDistinct)
	}
	if stats.Hits != 3 {
		t.Errorf("stats Hits = %d, want 3", stats.Hits)
	}
	if stats.LastSeen.IsZero() {
		t.Error("stats LastSeen is zero")
	}
}

// TestCSPReportStatsOnEmptyTable: max(last_seen) is NULL with no rows, which must not
// fail the scan — the admin page has to render before any report arrives.
func TestCSPReportStatsOnEmptyTable(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	stats, err := st.CSPReportStats(ctx)
	if err != nil {
		t.Fatalf("stats on an empty table: %v", err)
	}
	if stats.Distinct != 0 || stats.Hits != 0 || !stats.LastSeen.IsZero() || stats.AtCapacity {
		t.Errorf("stats = %+v, want zero values", stats)
	}
}

// TestPruneCSPReportsDropsStaleViolations: a violation that stops recurring should
// leave the page on its own, and one still being reported must survive.
func TestPruneCSPReportsDropsStaleViolations(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	old := cspReport("script-src-elem", "inline", "/ancient")
	old.LastSeen = time.Now().Add(-120 * 24 * time.Hour)
	fresh := cspReport("script-src-elem", "inline", "/current")
	if err := st.RecordCSPReports(ctx, []CSPReport{old, fresh}); err != nil {
		t.Fatalf("record: %v", err)
	}

	n, err := st.PruneCSPReports(ctx, 90)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d rows, want 1", n)
	}
	rows, _ := st.ListCSPReports(ctx, "", 10)
	if len(rows) != 1 || rows[0].DocumentPath != "/current" {
		t.Fatalf("rows after prune = %+v, want only /current", rows)
	}
}

func TestClearCSPReports(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	page := cspReport("script-src-elem", "inline", "/x")
	ext := cspReport("script-src-elem", "chrome-extension://abc", "/x")
	ext.Source = CSPReportSourceExtension
	if err := st.RecordCSPReports(ctx, []CSPReport{page, ext}); err != nil {
		t.Fatalf("record: %v", err)
	}

	// Clearing one source must leave the other alone: dropping extension noise
	// shouldn't discard real violations you haven't triaged.
	n, err := st.ClearCSPReports(ctx, CSPReportSourceExtension)
	if err != nil {
		t.Fatalf("clear extension: %v", err)
	}
	if n != 1 {
		t.Errorf("cleared %d rows, want 1", n)
	}
	rows, _ := st.ListCSPReports(ctx, "", 10)
	if len(rows) != 1 || rows[0].Source != CSPReportSourcePage {
		t.Fatalf("rows = %+v, want the page violation retained", rows)
	}

	if _, err := st.ClearCSPReports(ctx, ""); err != nil {
		t.Fatalf("clear all: %v", err)
	}
	rows, _ = st.ListCSPReports(ctx, "", 10)
	if len(rows) != 0 {
		t.Fatalf("rows after clearing all = %+v", rows)
	}
}

func TestValidCSPReportSource(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"page", "extension", "PAGE"} {
		if !ValidCSPReportSource(ok) {
			t.Errorf("ValidCSPReportSource(%q) = false", ok)
		}
	}
	// A rejected value must never reach a WHERE clause as a category we don't have.
	for _, bad := range []string{"", "all", "everything", "page'; DROP TABLE csp_reports--"} {
		if ValidCSPReportSource(bad) {
			t.Errorf("ValidCSPReportSource(%q) = true", bad)
		}
	}
}

// TestRecordCSPReportsHandlesEmptyBatch: the flusher calls this whenever its ticker
// fires, which is usually with nothing queued.
func TestRecordCSPReportsHandlesEmptyBatch(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	if err := st.RecordCSPReports(ctx, nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
}
