-- +goose Up
-- Record where the offending content came from, not just what was blocked.
--
-- Added after a real specimen showed the gap. An extension injecting an inline script
-- reports `blocked-uri: "inline"` — a bare keyword, byte-identical to what a genuine
-- inline-script XSS produces — so the classifier had nothing to distinguish them and
-- filed extension noise under "page". But the same report carried
-- `"source-file": "moz-extension"`, which is exactly the missing signal.
--
-- Note the shape: Firefox sends the BARE scheme, with no "://" and no extension ID.
-- That's deliberate on its part — an ID would let any site enumerate a visitor's
-- installed extensions. So matching has to accept a scheme with or without "://"
-- (see web.hasExtensionScheme).
--
-- source_file is normalized to scheme://host like blocked_uri, never the full URL: for
-- a violation on the login page the raw value carries the query string, and that
-- query holds a single-use auth code.
ALTER TABLE csp_reports ADD COLUMN source_file TEXT NOT NULL DEFAULT '';

-- The line the blocked content sits on. For violations in our own markup this plus
-- document_path is the difference between "something inline was blocked somewhere"
-- and a place to look. Deliberately NOT part of the fingerprint: line numbers shift
-- whenever a template changes, which would split one ongoing problem into a new row
-- per deploy.
ALTER TABLE csp_reports ADD COLUMN line_number INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE csp_reports DROP COLUMN line_number;
ALTER TABLE csp_reports DROP COLUMN source_file;
