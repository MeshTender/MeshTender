-- +goose Up
-- Records the most recent successful sign-in. NULL means the account has never
-- logged in since this column existed. Surfaced on the admin users page; also a
-- signal for when every password holder has logged in (and thus had their hash
-- migrated to the pre-hash scheme), so the legacy password fallback can retire.
ALTER TABLE users ADD COLUMN last_login_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE users DROP COLUMN last_login_at;
