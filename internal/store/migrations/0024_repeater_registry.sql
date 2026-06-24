-- +goose Up
-- The infrastructure-registry layer for repeaters: site documentation that
-- outlives the original builder, a maintenance history, designated backup
-- stewards, and an opt-in public page (the NFC/QR tap target).

-- public_map only ever meant "show on the public org page", never a standalone
-- map; rename it to say what it does now that a separate public *page* exists.
ALTER TABLE repeaters RENAME COLUMN public_map TO show_on_public_org;

-- expose_public_page publishes a read-only public page for this repeater at its
-- public_id URL (what an NFC tag or QR code inside the enclosure points at). It
-- is a distinct consent from show_on_public_org (a map pin on the org page).
ALTER TABLE repeaters ADD COLUMN expose_public_page BOOLEAN NOT NULL DEFAULT FALSE;

-- Documentation kept with the node. doc_public is safe for anyone who taps the
-- tag (what it is, how to service it); doc_internal holds sensitive site-access
-- details (gate codes, landlord contact) shown only to people with access.
ALTER TABLE repeaters ADD COLUMN doc_public   TEXT NOT NULL DEFAULT '';
ALTER TABLE repeaters ADD COLUMN doc_internal TEXT NOT NULL DEFAULT '';

-- A shared user can be flagged a steward: a designated co-maintainer listed on
-- the public page. Answers "who else can service this if the owner disappears".
ALTER TABLE repeater_shares ADD COLUMN steward BOOLEAN NOT NULL DEFAULT FALSE;

-- Manual maintenance history (distinct from the command_log, which is automatic).
-- Entries survive their author leaving (author_id goes NULL, author_name keeps a
-- readable record). performed_at is when the work happened; created_at when it
-- was logged.
CREATE TABLE repeater_maintenance (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repeater_id  BIGINT NOT NULL REFERENCES repeaters(id) ON DELETE CASCADE,
    author_id    BIGINT REFERENCES users(id) ON DELETE SET NULL,
    author_name  TEXT NOT NULL DEFAULT '',
    note         TEXT NOT NULL,
    performed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX repeater_maintenance_repeater_idx
    ON repeater_maintenance (repeater_id, performed_at DESC, id DESC);

-- +goose Down
DROP TABLE repeater_maintenance;
ALTER TABLE repeater_shares DROP COLUMN steward;
ALTER TABLE repeaters DROP COLUMN doc_internal;
ALTER TABLE repeaters DROP COLUMN doc_public;
ALTER TABLE repeaters DROP COLUMN expose_public_page;
ALTER TABLE repeaters RENAME COLUMN show_on_public_org TO public_map;
