-- +goose Up
-- The channels an org recommends its members join, listed in its Guide.
--
-- A name starting with '#' is a hashtag channel: its key is SHA256 of the exact
-- name (MeshCore's docs/companion_protocol.md), so none is stored and anyone may
-- see it. Any other name is a private channel with a random 16-byte key, sealed
-- with the server's master key (internal/secretbox) and shown only to members.
-- The CHECK ties the two together.
--
-- The suggested scope is advice for the member's app — the firmware has no
-- per-channel scope — and travels in the MeshCore app's channel QR code as
-- region_scope. scope_region_id NULL means "use your default scope"; unscoped
-- says to send unscoped instead. Deleting the region falls back to the default.
CREATE TABLE org_channels (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id          BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- The firmware's channel name is 32 bytes with its terminator.
    name            TEXT NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 31),
    secret_sealed   BYTEA,
    scope_region_id BIGINT REFERENCES config_regions(id) ON DELETE SET NULL,
    unscoped        BOOLEAN NOT NULL DEFAULT false,
    note            TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 200),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((left(name, 1) = '#') = (secret_sealed IS NULL)),
    CHECK (NOT (unscoped AND scope_region_id IS NOT NULL))
);
CREATE UNIQUE INDEX org_channels_org_name_idx ON org_channels (org_id, name);

-- +goose Down
DROP TABLE org_channels;
