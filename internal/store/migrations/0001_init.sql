-- +goose Up
CREATE TABLE users (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    -- Optional human-friendly name; display falls back to username when null.
    display_name  TEXT,
    password_hash TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE webauthn_credentials (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id BYTEA NOT NULL UNIQUE,
    -- Full marshaled webauthn.Credential (lossless for assertion verification,
    -- including sign count, AAGUID, transports, flags).
    data          JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX webauthn_credentials_user_id_idx ON webauthn_credentials(user_id);

-- Session store for alexedwards/scs (pgxstore).
CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BYTEA NOT NULL,
    expiry TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions (expiry);

-- Singleton row (id = 1) holding the one server-wide MeshCore identity.
CREATE TABLE server_identity (
    id             SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    public_key_hex TEXT NOT NULL,
    encrypted_seed BYTEA NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE repeaters (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    public_key_hex TEXT NOT NULL,
    radio_freq_hz  BIGINT NOT NULL,
    radio_bw_hz    BIGINT NOT NULL,
    radio_sf       SMALLINT NOT NULL,
    radio_cr       SMALLINT NOT NULL,
    confirmed      BOOLEAN NOT NULL DEFAULT FALSE,
    confirmed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_id, public_key_hex)
);
CREATE INDEX repeaters_owner_id_idx ON repeaters(owner_id);

CREATE TABLE repeater_shares (
    repeater_id BIGINT NOT NULL REFERENCES repeaters(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repeater_id, user_id)
);
CREATE INDEX repeater_shares_user_id_idx ON repeater_shares(user_id);

-- Single-use share links. An owner mints one labeled link per recipient; it is
-- consumed (used_at/used_by set) on first accept. Deleting a pending row
-- revokes that link. Used rows are retained as an audit trail.
CREATE TABLE repeater_invites (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repeater_id BIGINT NOT NULL REFERENCES repeaters(id) ON DELETE CASCADE,
    token       TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    used_at     TIMESTAMPTZ,
    used_by     BIGINT REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX repeater_invites_repeater_id_idx ON repeater_invites(repeater_id);

-- +goose Down
DROP TABLE repeater_invites;
DROP TABLE sessions;
DROP TABLE repeater_shares;
DROP TABLE repeaters;
DROP TABLE server_identity;
DROP TABLE webauthn_credentials;
DROP TABLE users;
