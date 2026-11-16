-- +goose Up
-- A one-line, plain-text summary of an organization, for wherever its long
-- markdown description won't fit: the public page's hero, the directory, and a
-- member's list of their organizations. Empty until an admin writes one; the
-- directory then falls back to the description.
ALTER TABLE organizations
    ADD COLUMN summary TEXT NOT NULL DEFAULT '' CHECK (char_length(summary) <= 160);

-- The directory's substring search covers the summary too; a trigram index, like
-- its siblings from 0033, keeps that from scanning the table.
CREATE INDEX organizations_summary_trgm_idx ON organizations USING gin (summary gin_trgm_ops);

-- +goose Down
DROP INDEX IF EXISTS organizations_summary_trgm_idx;
ALTER TABLE organizations DROP COLUMN summary;
