-- +goose Up
-- CSP violation reports, stored AGGREGATED rather than one row per report.
--
-- A row-per-report table doesn't survive contact with a real browser population.
-- Violations are overwhelmingly a small set of repeating causes, and a single
-- misbehaving browser extension on one visitor's machine can emit thousands of
-- identical reports a day. So each distinct violation is one row carrying a hit
-- counter: the table stays small enough to read on one screen, which is the whole
-- point of collecting it.
CREATE TABLE csp_reports (
    id BIGSERIAL PRIMARY KEY,
    -- Hash over the normalized identity of the violation (disposition, directive,
    -- blocked URI, document path). The UNIQUE constraint is what the upsert keys
    -- on, and it's a hash rather than a composite key so the index stays narrow
    -- regardless of how long a blocked URI is.
    fingerprint TEXT NOT NULL UNIQUE,
    -- 'enforce' (the live policy blocked it) or 'report' (a report-only policy
    -- would have). The distinction is the entire value of a report-only trial
    -- policy, so it's part of the fingerprint and never collapsed.
    disposition TEXT NOT NULL,
    directive TEXT NOT NULL,
    blocked_uri TEXT NOT NULL,
    -- Path only, never the query string: the login handoff carries a single-use
    -- auth code in the query, and a violation on that page would otherwise write a
    -- live credential into this table and render it on an admin screen. Invite
    -- tokens in the path are templatized for the same reason (web.RedactPath).
    document_path TEXT NOT NULL,
    host TEXT NOT NULL,
    -- 'page' (worth investigating) or 'extension' (a browser add-on injecting into
    -- someone's page — noise we didn't cause and can't fix). Classified rather than
    -- dropped, so there's a record to check the next time an extension is mistaken
    -- for a real bug.
    source TEXT NOT NULL,
    -- A truncated snippet of the offending content. Browsers already cap this at
    -- ~40 chars; it's page content either way, so it's truncated again on write.
    sample TEXT NOT NULL DEFAULT '',
    hits BIGINT NOT NULL DEFAULT 1,
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The admin view reads "most recent first", optionally filtered to one source.
-- source leads so the filtered read is a single index range.
CREATE INDEX csp_reports_source_last_seen_idx ON csp_reports (source, last_seen DESC);
CREATE INDEX csp_reports_last_seen_idx ON csp_reports (last_seen DESC);

-- +goose Down
DROP TABLE csp_reports;
