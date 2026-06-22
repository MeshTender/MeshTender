-- +goose Up
-- An org's recommended configuration ("desired state"): a versioned, one-per-org
-- profile of firmware CLI command steps an operator should run to bring a repeater
-- in spec. Append-only versions mirror org_permission_versions so published
-- references stay stable. Profiles are NOT seeded on org creation — an org has no
-- profile until an admin publishes one.
CREATE TABLE config_profile_versions (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id     BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    version    INT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, version)
);

-- A location zone within a profile version: a named geofence whose steps apply
-- only to repeaters inside it. The geofence is a GeoJSON Polygon/MultiPolygon
-- (NULL = matches everywhere). Membership is point-in-polygon, so non-rectangular
-- zones need no schema change. There is intentionally NO unique constraint on
-- priority — same-priority zones may overlap, and a repeater in the overlap gets
-- every matching zone's steps.
CREATE TABLE config_zones (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    version_id BIGINT NOT NULL REFERENCES config_profile_versions(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    priority   INT NOT NULL DEFAULT 0,  -- application order: lower = outer/earlier
    geofence   JSONB,                   -- GeoJSON Polygon/MultiPolygon; NULL = matches everywhere
    UNIQUE (version_id, name)
);

-- An ordered command step in a profile version. zone_id NULL = a base step applied
-- to every repeater; otherwise it applies only inside that zone. command_id is the
-- resolved catalog match (NULL for comment-only steps).
CREATE TABLE config_profile_steps (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    version_id   BIGINT NOT NULL REFERENCES config_profile_versions(id) ON DELETE CASCADE,
    zone_id      BIGINT REFERENCES config_zones(id) ON DELETE CASCADE,
    position     INT NOT NULL,
    command_line TEXT NOT NULL DEFAULT '',
    command_id   BIGINT REFERENCES command_catalog(id) ON DELETE SET NULL,
    comment      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX config_profile_steps_version_zone_idx
    ON config_profile_steps (version_id, zone_id, position);

-- +goose Down
DROP TABLE config_profile_steps;
DROP TABLE config_zones;
DROP TABLE config_profile_versions;
