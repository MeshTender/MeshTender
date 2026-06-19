-- +goose Up
-- Access level learned from the repeater's login reply when confirmed.
-- NULL = not yet determined (confirmed before access tracking, or never tested).
ALTER TABLE repeaters ADD COLUMN confirmed_admin BOOLEAN;
ALTER TABLE repeaters ADD COLUMN confirmed_perms SMALLINT;

-- +goose Down
ALTER TABLE repeaters DROP COLUMN confirmed_perms;
ALTER TABLE repeaters DROP COLUMN confirmed_admin;
