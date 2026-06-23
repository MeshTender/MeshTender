-- +goose Up
-- Drop the separate "store this repeater's location" opt-in. Location is now
-- always stored when the modem test fetches it, and the public_map flag (now
-- "show on the public org page") is the only location-related visibility control —
-- it no longer depends on a store-location consent.
ALTER TABLE repeaters DROP COLUMN store_location;

-- +goose Down
ALTER TABLE repeaters ADD COLUMN store_location BOOLEAN NOT NULL DEFAULT false;
