-- +goose Up
-- Optional human-friendly label for a passkey, shown on the account page so a
-- user can tell their credentials apart (e.g. "MacBook Touch ID", "YubiKey").
ALTER TABLE webauthn_credentials ADD COLUMN name TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE webauthn_credentials DROP COLUMN name;
