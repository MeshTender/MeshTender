-- +goose Up
-- Freeform region/location for an organization (e.g. "Buffalo, NY"), shown on
-- the org page and public directory.
ALTER TABLE organizations ADD COLUMN region TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE organizations DROP COLUMN region;
