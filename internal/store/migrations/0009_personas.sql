-- +goose Up
-- Organizations get a public-facing description (orgs are publicly viewable).
ALTER TABLE organizations ADD COLUMN description TEXT NOT NULL DEFAULT '';

-- Per-repeater opt-in to appear on the *public* org map. Distinct from
-- store_location: storing coordinates (for members) does not imply publishing
-- them on a page anonymous visitors can see.
ALTER TABLE repeaters ADD COLUMN public_map BOOLEAN NOT NULL DEFAULT FALSE;

-- Per-user confirmation history. Each successful login round-trip records who
-- reached the repeater and the access learned. The owner's own row is a
-- "self-confirmation"; a row by anyone else "corroborates" that MeshTender can
-- reach the node. The repeaters.confirmed* columns remain a cached "latest".
CREATE TABLE repeater_confirmations (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repeater_id  BIGINT NOT NULL REFERENCES repeaters(id) ON DELETE CASCADE,
    user_id      BIGINT REFERENCES users(id) ON DELETE SET NULL,
    is_admin     BOOLEAN NOT NULL,
    perms        SMALLINT NOT NULL,
    confirmed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX repeater_confirmations_repeater_id_idx ON repeater_confirmations(repeater_id);

-- +goose Down
DROP TABLE repeater_confirmations;
ALTER TABLE repeaters DROP COLUMN public_map;
ALTER TABLE organizations DROP COLUMN description;
