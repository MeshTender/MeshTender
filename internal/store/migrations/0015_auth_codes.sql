-- +goose Up
-- Single-use codes that hand off an authenticated session from the auth host to
-- the app host. The auth host mints a row after a successful sign-in and
-- redirects the browser to the app host's callback, which consumes it (deleting
-- the row) to establish its own host-scoped session. Short-lived and one-shot.
CREATE TABLE auth_codes (
    code    TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    next    TEXT NOT NULL DEFAULT '/',  -- validated app-local post-auth path
    expiry  TIMESTAMPTZ NOT NULL
);
CREATE INDEX auth_codes_expiry_idx ON auth_codes (expiry);

-- +goose Down
DROP TABLE auth_codes;
