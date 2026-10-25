-- +goose Up
-- Companion check-ins: members register their companion radios, orgs point
-- MeshTender at the MQTT broker their observers uplink to, and every verified
-- advert a registered companion sends is recorded as it is heard. Weekly
-- check-in nets read their rosters straight out of those sightings.

-- One MQTT broker per org. The password is sealed under MESHTENDER_MASTER_KEY
-- (internal/secretbox) and never read back to the UI. The status columns are
-- written by the ingester (throttled) so every replica can render them;
-- last_error is a coarse category, never a raw dial error.
CREATE TABLE org_mqtt_brokers (
    org_id            BIGINT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    url               TEXT NOT NULL,
    username          TEXT NOT NULL DEFAULT '',
    password_sealed   BYTEA,
    topic_prefix      TEXT NOT NULL DEFAULT 'meshcore',
    enabled           BOOLEAN NOT NULL DEFAULT TRUE,
    updated_by        BIGINT REFERENCES users(id) ON DELETE SET NULL,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_connected_at TIMESTAMPTZ,
    last_message_at   TIMESTAMPTZ,
    last_error        TEXT NOT NULL DEFAULT ''
);

-- An observer is an uplinking node an org vouches for: only packets that arrive
-- on the org's broker under a registered observer's topic are ingested. The same
-- physical observer may be registered by several orgs. Health comes from the
-- observer's own status topic.
CREATE TABLE org_observers (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id           BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    public_key_hex   TEXT NOT NULL,
    created_by       BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at     TIMESTAMPTZ,
    client_version   TEXT NOT NULL DEFAULT '',
    firmware_version TEXT NOT NULL DEFAULT '',
    model            TEXT NOT NULL DEFAULT '',
    battery_mv       INTEGER,
    noise_floor      INTEGER,
    UNIQUE (org_id, public_key_hex)
);
CREATE INDEX org_observers_key_idx ON org_observers (public_key_hex);

-- A companion belongs to its owner, and through them to every org they're a
-- member of (the same implicit rule as repeaters). The key is unique across all
-- users: an advert proves who sent it, so one key can only be one person's.
-- confirmed_at is set by the first verified advert heard after registration;
-- unconfirmed rows expire so a mistyped or squatted key frees itself.
CREATE TABLE companions (
    id                  BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id           TEXT NOT NULL UNIQUE,
    owner_id            BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    public_key_hex      TEXT NOT NULL UNIQUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at        TIMESTAMPTZ,
    last_heard_at       TIMESTAMPTZ,
    last_advert_ts      BIGINT,
    -- Feedback for the owner when an advert was heard but its clock was too far
    -- off to count (the replay guard).
    last_rejected_at    TIMESTAMPTZ,
    last_rejected_skew_s INTEGER
);
CREATE INDEX companions_owner_idx ON companions (owner_id, lower(name), id);

-- One row per (observer, route) an advert was heard on. A single flood advert
-- yields many rows — every observer, every path it took — which is what the
-- route and coverage views are built from. The unique key carries no org, so
-- uplink duplicates, two orgs sharing a broker, and replica handover all
-- collapse to one row. path is the flattened hash list (path_hash_size bytes
-- each); empty for zero-hop or a flood heard straight from the sender.
CREATE TABLE advert_sightings (
    id                     BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    companion_id           BIGINT NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
    observer_public_key_hex TEXT NOT NULL,
    received_at            TIMESTAMPTZ NOT NULL,
    advert_ts              BIGINT NOT NULL,
    route_type             SMALLINT NOT NULL,
    path_hash_size         SMALLINT NOT NULL,
    path                   BYTEA NOT NULL,
    snr                    REAL,
    rssi                   SMALLINT,
    UNIQUE (companion_id, observer_public_key_hex, advert_ts, path)
);
CREATE INDEX advert_sightings_companion_idx ON advert_sightings (companion_id, received_at DESC, id DESC);
CREATE INDEX advert_sightings_received_idx ON advert_sightings (received_at);

-- A check-in net recurs weekly at a local wall-clock time in its own timezone.
-- weekday follows Go's time.Weekday (0 = Sunday); start_minute is minutes after
-- local midnight.
CREATE TABLE check_in_nets (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id           BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    weekday          SMALLINT NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    start_minute     SMALLINT NOT NULL CHECK (start_minute BETWEEN 0 AND 1439),
    duration_minutes INTEGER NOT NULL CHECK (duration_minutes BETWEEN 1 AND 1440),
    timezone         TEXT NOT NULL,
    active           BOOLEAN NOT NULL DEFAULT TRUE,
    created_by       BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX check_in_nets_org_idx ON check_in_nets (org_id, lower(name), id);

-- Sessions are materialized ahead of time so a schedule edit only moves sessions
-- that haven't started: a past roster keeps the window it was run in. The roster
-- itself is not stored; it's a query over advert_sightings in [starts_at, ends_at).
CREATE TABLE net_sessions (
    id        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    net_id    BIGINT NOT NULL REFERENCES check_in_nets(id) ON DELETE CASCADE,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at   TIMESTAMPTZ NOT NULL,
    UNIQUE (net_id, starts_at)
);

-- +goose Down
DROP TABLE net_sessions;
DROP TABLE check_in_nets;
DROP TABLE advert_sightings;
DROP TABLE companions;
DROP TABLE org_observers;
DROP TABLE org_mqtt_brokers;
