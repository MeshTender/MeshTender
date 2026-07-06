-- +goose Up
-- The user's preferred IANA time zone (e.g. "America/New_York") for displaying
-- dates and times. Empty means "not set" — the browser auto-detects the viewer's
-- zone client-side. Times are stored in UTC (TIMESTAMPTZ) and localized at
-- display time; this column only affects presentation.
ALTER TABLE users ADD COLUMN timezone TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE users DROP COLUMN timezone;
