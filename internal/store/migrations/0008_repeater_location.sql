-- +goose Up
-- Opt-in location storage. latitude/longitude are fetched from the repeater
-- during the modem test only when store_location is set.
ALTER TABLE repeaters ADD COLUMN store_location BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE repeaters ADD COLUMN latitude  DOUBLE PRECISION;
ALTER TABLE repeaters ADD COLUMN longitude DOUBLE PRECISION;

-- +goose Down
ALTER TABLE repeaters DROP COLUMN longitude;
ALTER TABLE repeaters DROP COLUMN latitude;
ALTER TABLE repeaters DROP COLUMN store_location;
