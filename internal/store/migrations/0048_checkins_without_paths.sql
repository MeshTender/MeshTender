-- +goose Up
-- Check-ins keep counts, not paths. advert_sightings stored every route an
-- advert took to every observer — the ordered list of repeaters, which says
-- roughly where a member is. The roster only needs to know that a member's
-- advert was heard, when, by how many observers, and how far it spread.

DROP TABLE advert_sightings;

-- One row per advert a registered companion sent: the permanent record behind
-- last-heard, confirmation and net rosters. Counts only.
CREATE TABLE advert_checkins (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    companion_id   BIGINT NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
    -- The sender's own clock on the advert; with the companion, identifies it.
    advert_ts      BIGINT NOT NULL,
    first_heard_at TIMESTAMPTZ NOT NULL,
    -- True while every copy heard was a zero-hop advert.
    zero_hop       BOOLEAN NOT NULL,
    -- Distinct observers that heard it, and distinct repeaters seen relaying it.
    observers      INTEGER NOT NULL DEFAULT 0,
    spread         INTEGER NOT NULL DEFAULT 0,
    UNIQUE (companion_id, advert_ts)
);
CREATE INDEX advert_checkins_companion_idx ON advert_checkins (companion_id, first_heard_at DESC, id DESC);
CREATE INDEX advert_checkins_heard_idx ON advert_checkins (first_heard_at);

-- While an advert is still propagating, the sets behind its counts: which
-- repeaters relayed it and which observers heard it. Unordered by design — no
-- per-row time or sequence, so they can't say which repeater came first — and
-- deleted AdvertSetTTL after the advert was first heard (the ingester sweeps
-- them). UNLOGGED keeps them out of the write-ahead log, so they're never
-- replicated or written into a backup; a crash or failover empties them, which
-- costs at most the counts of adverts still in flight.
CREATE UNLOGGED TABLE advert_relays (
    checkin_id BIGINT NOT NULL REFERENCES advert_checkins(id) ON DELETE CASCADE,
    hash       BYTEA NOT NULL, -- one path entry: a repeater's 1–3 byte hash
    PRIMARY KEY (checkin_id, hash)
);

CREATE UNLOGGED TABLE advert_receivers (
    checkin_id              BIGINT NOT NULL REFERENCES advert_checkins(id) ON DELETE CASCADE,
    observer_public_key_hex TEXT NOT NULL,
    PRIMARY KEY (checkin_id, observer_public_key_hex)
);

-- +goose Down
DROP TABLE advert_receivers;
DROP TABLE advert_relays;
DROP TABLE advert_checkins;
CREATE TABLE advert_sightings (
    id                      BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    companion_id            BIGINT NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
    observer_public_key_hex TEXT NOT NULL,
    received_at             TIMESTAMPTZ NOT NULL,
    advert_ts               BIGINT NOT NULL,
    route_type              SMALLINT NOT NULL,
    path_hash_size          SMALLINT NOT NULL,
    path                    BYTEA NOT NULL,
    snr                     REAL,
    rssi                    SMALLINT,
    UNIQUE (companion_id, observer_public_key_hex, advert_ts, path)
);
CREATE INDEX advert_sightings_companion_idx ON advert_sightings (companion_id, received_at DESC, id DESC);
CREATE INDEX advert_sightings_received_idx ON advert_sightings (received_at);
