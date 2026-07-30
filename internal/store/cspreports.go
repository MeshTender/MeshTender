package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// MaxDistinctCSPReports caps how many distinct violations the table will hold.
//
// The endpoint is necessarily public and unauthenticated — browsers send reports
// with no credentials — so an upsert keyed on a fingerprint is, without a cap, an
// unbounded insert vector: vary one field per request and every report becomes a
// new row. The cap converts that into a bounded nuisance.
//
// It applies to NEW fingerprints only; hits on violations already recorded keep
// counting (see RecordCSPReports). The failure mode when the cap is reached is that
// genuinely new violations stop being recorded, which is why the report path is also
// rate-limited per IP and filtered to our own hosts before it gets here — reaching
// this ceiling should take deliberate effort, and the admin page shows the count so
// a full table is visible rather than silent.
const MaxDistinctCSPReports = 500

// CSPReportSourcePage and CSPReportSourceExtension classify where a violation came
// from. Extension noise is by far the bulk of real-world CSP reporting, and it's
// something we neither caused nor can fix, so the admin view defaults to hiding it.
const (
	CSPReportSourcePage      = "page"
	CSPReportSourceExtension = "extension"
)

// CSPReport is one normalized violation, ready to be counted. Hits is the number of
// occurrences this instance represents (>1 when a batch folded duplicates).
type CSPReport struct {
	Disposition  string    `json:"disposition"`
	Directive    string    `json:"directive"`
	BlockedURI   string    `json:"blockedUri"`
	DocumentPath string    `json:"documentPath"`
	Host         string    `json:"host"`
	Source       string    `json:"source"`
	Sample       string    `json:"sample"`
	SourceFile   string    `json:"sourceFile"`
	LineNumber   int       `json:"lineNumber"`
	Hits         int64     `json:"hits"`
	LastSeen     time.Time `json:"lastSeen"`
}

// Fingerprint is the identity of a violation: two reports with the same fingerprint
// are the same problem and are counted together.
//
// Deliberately excludes Sample, SourceFile, LineNumber and Hits. The sample varies
// between otherwise identical violations (different inline snippets on the same page)
// and the line number moves whenever a template changes, so including either would
// shatter one problem into many rows — exactly the row-per-report behaviour the
// aggregate table exists to avoid. SourceFile is omitted as redundant: it already
// drives Source, which IS in the fingerprint, so an extension-injected inline
// violation and a page one on the same path are already separate rows.
func (r CSPReport) Fingerprint() string {
	h := sha256.New()
	// Length-prefixed so no two field boundaries can be confused with each other.
	// hash.Hash.Write never returns an error, so Fprintf's can't either.
	for _, f := range []string{r.Disposition, r.Directive, r.BlockedURI, r.DocumentPath, r.Host, r.Source} {
		_, _ = fmt.Fprintf(h, "%d:%s|", len(f), f)
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// CSPReportRow is one stored violation as the admin view reads it.
type CSPReportRow struct {
	ID           int64     `json:"id"`
	Disposition  string    `json:"disposition"`
	Directive    string    `json:"directive"`
	BlockedURI   string    `json:"blockedUri"`
	DocumentPath string    `json:"documentPath"`
	Host         string    `json:"host"`
	Source       string    `json:"source"`
	Sample       string    `json:"sample"`
	SourceFile   string    `json:"sourceFile"`
	LineNumber   int       `json:"lineNumber"`
	Hits         int64     `json:"hits"`
	FirstSeen    time.Time `json:"firstSeen"`
	LastSeen     time.Time `json:"lastSeen"`
}

// CSPReportStats summarizes the table for the admin page's header.
type CSPReportStats struct {
	Distinct          int64     `json:"distinct"`
	Hits              int64     `json:"hits"`
	PageDistinct      int64     `json:"pageDistinct"`
	ExtensionDistinct int64     `json:"extensionDistinct"`
	LastSeen          time.Time `json:"lastSeen"`
	// AtCapacity reports that the distinct-fingerprint cap is reached, so new
	// violations are being dropped. Surfaced on the page because a silently full
	// table looks identical to a clean one.
	AtCapacity bool `json:"atCapacity"`
}

// recordCSPReport is the upsert. The WHERE clause enforces MaxDistinctCSPReports on
// new fingerprints while always letting an existing one through to the ON CONFLICT
// update — the naive `WHERE count < cap` alone would also stop counting hits on
// violations already in the table once it filled.
const recordCSPReport = `
	INSERT INTO csp_reports
	    (fingerprint, disposition, directive, blocked_uri, document_path, host, source, sample,
	     source_file, line_number, hits, first_seen, last_seen)
	SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12
	WHERE (SELECT count(*) FROM csp_reports) < $13
	   OR EXISTS (SELECT 1 FROM csp_reports WHERE fingerprint = $1)
	ON CONFLICT (fingerprint) DO UPDATE SET
	    hits = csp_reports.hits + EXCLUDED.hits,
	    last_seen = GREATEST(csp_reports.last_seen, EXCLUDED.last_seen),
	    -- Keep the first sample/source seen rather than the newest: they're debugging
	    -- hints, and stable ones are more useful than ones that churn on every hit.
	    sample = CASE WHEN csp_reports.sample = '' THEN EXCLUDED.sample ELSE csp_reports.sample END,
	    source_file = CASE WHEN csp_reports.source_file = '' THEN EXCLUDED.source_file ELSE csp_reports.source_file END,
	    line_number = CASE WHEN csp_reports.line_number = 0 THEN EXCLUDED.line_number ELSE csp_reports.line_number END`

// RecordCSPReports counts a batch of violations, folding duplicates first so a burst
// of identical reports costs one statement rather than one per report.
func (s *Store) RecordCSPReports(ctx context.Context, reps []CSPReport) error {
	if len(reps) == 0 {
		return nil
	}
	folded := make(map[string]CSPReport, len(reps))
	order := make([]string, 0, len(reps))
	for _, r := range reps {
		if r.Hits <= 0 {
			r.Hits = 1
		}
		fp := r.Fingerprint()
		prev, ok := folded[fp]
		if !ok {
			folded[fp] = r
			order = append(order, fp)
			continue
		}
		prev.Hits += r.Hits
		if r.LastSeen.After(prev.LastSeen) {
			prev.LastSeen = r.LastSeen
		}
		if prev.Sample == "" {
			prev.Sample = r.Sample
		}
		if prev.SourceFile == "" {
			prev.SourceFile = r.SourceFile
		}
		if prev.LineNumber == 0 {
			prev.LineNumber = r.LineNumber
		}
		folded[fp] = prev
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		for _, fp := range order {
			r := folded[fp]
			if _, err := tx.Exec(ctx, recordCSPReport,
				fp, r.Disposition, r.Directive, r.BlockedURI, r.DocumentPath, r.Host,
				r.Source, r.Sample, r.SourceFile, r.LineNumber, r.Hits, r.LastSeen,
				MaxDistinctCSPReports); err != nil {
				return fmt.Errorf("record csp report: %w", err)
			}
		}
		return nil
	})
}

// ListCSPReports returns stored violations, newest activity first. source filters to
// one classification ("" for all); limit bounds the page.
func (s *Store) ListCSPReports(ctx context.Context, source string, limit int) ([]CSPReportRow, error) {
	if limit <= 0 {
		limit = 100
	}
	// A single query with a NULL-means-all predicate rather than two near-identical
	// strings; source is indexed and the planner still uses it when non-NULL.
	var src *string
	if source != "" {
		src = &source
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, disposition, directive, blocked_uri, document_path, host, source,
		       sample, source_file, line_number, hits, first_seen, last_seen
		FROM csp_reports
		WHERE ($1::text IS NULL OR source = $1)
		ORDER BY last_seen DESC
		LIMIT $2`, src, limit)
	if err != nil {
		return nil, fmt.Errorf("list csp reports: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (CSPReportRow, error) {
		var c CSPReportRow
		return c, r.Scan(&c.ID, &c.Disposition, &c.Directive, &c.BlockedURI, &c.DocumentPath,
			&c.Host, &c.Source, &c.Sample, &c.SourceFile, &c.LineNumber, &c.Hits,
			&c.FirstSeen, &c.LastSeen)
	})
}

// CSPReportStats summarizes the whole table (which the distinct cap keeps small, so
// this stays a trivial aggregate over a few hundred rows).
func (s *Store) CSPReportStats(ctx context.Context) (CSPReportStats, error) {
	var st CSPReportStats
	var last *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT count(*),
		       coalesce(sum(hits), 0),
		       count(*) FILTER (WHERE source = $1),
		       count(*) FILTER (WHERE source = $2),
		       max(last_seen)
		FROM csp_reports`, CSPReportSourcePage, CSPReportSourceExtension).
		Scan(&st.Distinct, &st.Hits, &st.PageDistinct, &st.ExtensionDistinct, &last)
	if err != nil {
		return st, fmt.Errorf("csp report stats: %w", err)
	}
	if last != nil {
		st.LastSeen = *last
	}
	st.AtCapacity = st.Distinct >= MaxDistinctCSPReports
	return st, nil
}

// PruneCSPReports drops violations not seen for keepDays, so a problem that was
// fixed eventually disappears instead of sitting on the page forever. Runs on the
// janitor.
func (s *Store) PruneCSPReports(ctx context.Context, keepDays int) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM csp_reports WHERE last_seen < now() - ($1::int * interval '1 day')`, keepDays)
	if err != nil {
		return 0, fmt.Errorf("prune csp reports: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ClearCSPReports deletes stored violations, optionally just one source. This is the
// triage action: after shipping a fix you clear the row and watch whether it returns.
func (s *Store) ClearCSPReports(ctx context.Context, source string) (int64, error) {
	var src *string
	if source != "" {
		src = &source
	}
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM csp_reports WHERE ($1::text IS NULL OR source = $1)`, src)
	if err != nil {
		return 0, fmt.Errorf("clear csp reports: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ValidCSPReportSource reports whether s is a source filter we recognize, so a
// query-string value can be used in a WHERE clause without inventing a category.
func ValidCSPReportSource(s string) bool {
	switch strings.ToLower(s) {
	case CSPReportSourcePage, CSPReportSourceExtension:
		return true
	}
	return false
}
