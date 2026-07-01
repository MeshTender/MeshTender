-- +goose Up
-- Public profile fields for a user's page (/u/{username}). All optional — a blank
-- field simply doesn't render, which is how users keep information private.
ALTER TABLE users ADD COLUMN bio      TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN location TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN callsign TEXT NOT NULL DEFAULT '';

-- Contact/social links shown on a user's public page, mirroring org_links. One
-- link may be flagged is_primary as the preferred way to reach the user (drives
-- the "add a contact link" nudge for people listed publicly). The meshcore
-- platform's url column holds a MeshCore public key rather than an http URL and
-- renders as a QR code.
CREATE TABLE user_links (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform   TEXT NOT NULL,
    label      TEXT NOT NULL DEFAULT '',
    url        TEXT NOT NULL,
    position   INT NOT NULL DEFAULT 0,
    is_primary BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX user_links_user_idx ON user_links(user_id, position);

-- +goose Down
DROP TABLE user_links;
ALTER TABLE users DROP COLUMN callsign;
ALTER TABLE users DROP COLUMN location;
ALTER TABLE users DROP COLUMN bio;
