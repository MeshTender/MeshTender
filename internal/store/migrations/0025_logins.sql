-- +goose Up
-- A login is one real sign-in by a user. Every per-host session that springs
-- from it (app, auth, root identity beacon, custom org domains) stores this id
-- and is validated against this row on each request, so revoking it here logs
-- the user out of every host at once. See docs/auth-cross-host.md.
CREATE TABLE logins (
    id         TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label      TEXT NOT NULL DEFAULT '',  -- reserved for a future "your devices" list
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);
CREATE INDEX logins_user_id_idx ON logins (user_id);

-- The handoff code now also threads the login id, so the app host's callback
-- (and the root beacon) reuse the auth host's login row instead of minting a
-- second one — keeping exactly one revocable row per real sign-in.
ALTER TABLE auth_codes ADD COLUMN login_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE auth_codes DROP COLUMN login_id;
DROP TABLE logins;
