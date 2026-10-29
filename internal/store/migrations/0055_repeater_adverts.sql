-- +goose Up
-- Repeaters as heard on the air: what a registered repeater's own verified
-- adverts say about it (observed state), next to what its owner registered.
-- Keyed by public key, not repeater id: two owners can register the same key,
-- and an advert says the same thing to every org that hears it.

-- Adverts are matched to repeaters by key; the unique index leads with owner_id.
CREATE INDEX repeaters_public_key_idx ON repeaters (public_key_hex);

-- One row per advert (key + sender timestamp): every observer's copy, and any
-- replay of one we already have, lands on the same row. Flood rows are kept
-- long enough to estimate the flood advert interval at its 168 h maximum;
-- zero-hop rows only briefly (see store.RepeaterAdvertRetention).
CREATE TABLE repeater_adverts (
    public_key_hex TEXT        NOT NULL,
    advert_ts      BIGINT      NOT NULL,
    zero_hop       BOOLEAN     NOT NULL,
    first_heard_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (public_key_hex, advert_ts)
);
CREATE INDEX repeater_adverts_heard_idx ON repeater_adverts (first_heard_at);

-- The latest of it all, one row per key.
CREATE TABLE repeater_observations (
    public_key_hex   TEXT PRIMARY KEY,
    last_heard_at    TIMESTAMPTZ NOT NULL,
    last_flood_at    TIMESTAMPTZ,
    last_zero_hop_at TIMESTAMPTZ,
    -- From the newest advert heard. By our clock, not the sender's: a repeater
    -- whose clock resets on reboot goes back to 2024 every time.
    advert_name      TEXT        NOT NULL DEFAULT '',
    advert_lat       DOUBLE PRECISION,
    advert_lon       DOUBLE PRECISION,
    -- Heard time minus the sender's timestamp: positive when its clock is behind.
    clock_offset_s   BIGINT      NOT NULL DEFAULT 0,
    -- From the newest flood advert: zero-hop adverts don't carry the hash size,
    -- and only floods are scoped. The packet is kept so its scope can be matched
    -- against the regions that exist when it's shown.
    path_hash_size   SMALLINT,
    last_flood_packet BYTEA,
    flood_interval_h       INTEGER,
    flood_interval_samples INTEGER NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE repeater_observations;
DROP TABLE repeater_adverts;
DROP INDEX repeaters_public_key_idx;
