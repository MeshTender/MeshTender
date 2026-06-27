-- +goose Up
-- A region can be marked primary; the config page frames its location-preview
-- map on the primary region (falling back to the org's repeaters when none is set).
ALTER TABLE config_regions ADD COLUMN is_primary BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE config_regions DROP COLUMN is_primary;
