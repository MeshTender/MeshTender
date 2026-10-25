-- +goose Up
-- On the air: how net control's modem talks to the net. Announcements and
-- acknowledgements go out as channel messages (GRP_TXT) on the net's channel,
-- sent as on_air_name, optionally scoped to one of the org's regions so
-- repeaters that deny unscoped floods still carry them. Channel messages
-- starting with log_prefix are logged to the session (empty logs nothing).
-- The radio itself is the org's (organizations.radio_*).
ALTER TABLE check_in_nets
    -- public: the default Public channel. hashtag: "#channel_name", keyed by
    -- its name. private: a shared key, sealed under MESHTENDER_MASTER_KEY
    -- (internal/secretbox) and never read back to the UI; channel_name is
    -- then just a label.
    ADD COLUMN channel_kind       TEXT NOT NULL DEFAULT 'public'
        CHECK (channel_kind IN ('public', 'hashtag', 'private')),
    ADD COLUMN channel_name       TEXT NOT NULL DEFAULT '',
    ADD COLUMN channel_psk_sealed BYTEA,
    -- The org's region; the store checks it's the same org's. A deleted
    -- region leaves the net unscoped.
    ADD COLUMN scope_region_id    BIGINT REFERENCES config_regions(id) ON DELETE SET NULL,
    -- Empty means the net's name.
    ADD COLUMN on_air_name        TEXT NOT NULL DEFAULT '',
    ADD COLUMN log_prefix         TEXT NOT NULL DEFAULT 'news:',
    ADD CONSTRAINT check_in_nets_channel_complete CHECK (
        (channel_kind <> 'hashtag' OR channel_name <> '') AND
        (channel_kind <> 'private' OR channel_psk_sealed IS NOT NULL));

-- The name a companion advertises (signed, public, from its latest advert):
-- what other companions list it as, so it's what an on-air acknowledgement
-- mentions (@[name]). The member's own label for it stays in name.
ALTER TABLE companions ADD COLUMN advert_name TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE companions DROP COLUMN advert_name;
ALTER TABLE check_in_nets
    DROP CONSTRAINT check_in_nets_channel_complete,
    DROP COLUMN log_prefix,
    DROP COLUMN on_air_name,
    DROP COLUMN scope_region_id,
    DROP COLUMN channel_psk_sealed,
    DROP COLUMN channel_name,
    DROP COLUMN channel_kind;
