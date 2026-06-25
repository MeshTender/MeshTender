-- +goose Up
-- Restructure org configuration into multiple named, mutable profiles (base
-- settings) plus org-level regions (location steps). Profiles and regions are
-- independent: configuring a repeater means picking a profile for its base
-- settings, while region steps are added purely by location — the two never
-- interact. This replaces the single versioned profile-with-zones model; each
-- org's latest legacy profile migrates into a "Default" profile and org regions.

ALTER TABLE config_profile_steps    RENAME TO legacy_config_steps;
ALTER TABLE config_zones            RENAME TO legacy_config_zones;
ALTER TABLE config_profile_versions RENAME TO legacy_config_versions;

-- Named base-settings profiles, edited in place (no versioning).
CREATE TABLE config_profiles (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id     BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    position   INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);
CREATE TABLE config_profile_steps (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    profile_id   BIGINT NOT NULL REFERENCES config_profiles(id) ON DELETE CASCADE,
    position     INT NOT NULL,
    command_line TEXT NOT NULL DEFAULT '',
    command_id   BIGINT REFERENCES command_catalog(id) ON DELETE SET NULL,
    comment      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX config_profile_steps_profile_idx ON config_profile_steps (profile_id, position);

-- Org-level regions: geofenced location steps, independent of profiles.
CREATE TABLE config_regions (
    id        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id    BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name      TEXT NOT NULL,
    priority  INT NOT NULL DEFAULT 0,  -- application order: lower = earlier
    geofence  JSONB,                   -- GeoJSON Polygon/MultiPolygon; NULL = everywhere
    UNIQUE (org_id, name)
);
CREATE TABLE config_region_steps (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    region_id    BIGINT NOT NULL REFERENCES config_regions(id) ON DELETE CASCADE,
    position     INT NOT NULL,
    command_line TEXT NOT NULL DEFAULT '',
    command_id   BIGINT REFERENCES command_catalog(id) ON DELETE SET NULL,
    comment      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX config_region_steps_region_idx ON config_region_steps (region_id, position);

-- Migrate each org's latest legacy version into a "Default" profile + regions.
INSERT INTO config_profiles (org_id, name, position)
SELECT DISTINCT v.org_id, 'Default', 0
FROM legacy_config_versions v
JOIN (SELECT org_id, max(version) AS version FROM legacy_config_versions GROUP BY org_id) l
  ON l.org_id = v.org_id AND l.version = v.version;

INSERT INTO config_profile_steps (profile_id, position, command_line, command_id, comment)
SELECT p.id, s.position, s.command_line, s.command_id, s.comment
FROM legacy_config_steps s
JOIN legacy_config_versions v ON v.id = s.version_id
JOIN (SELECT org_id, max(version) AS version FROM legacy_config_versions GROUP BY org_id) l
  ON l.org_id = v.org_id AND l.version = v.version
JOIN config_profiles p ON p.org_id = v.org_id AND p.name = 'Default'
WHERE s.zone_id IS NULL;

INSERT INTO config_regions (org_id, name, priority, geofence)
SELECT v.org_id, z.name, z.priority, z.geofence
FROM legacy_config_zones z
JOIN legacy_config_versions v ON v.id = z.version_id
JOIN (SELECT org_id, max(version) AS version FROM legacy_config_versions GROUP BY org_id) l
  ON l.org_id = v.org_id AND l.version = v.version;

INSERT INTO config_region_steps (region_id, position, command_line, command_id, comment)
SELECT cr.id, s.position, s.command_line, s.command_id, s.comment
FROM legacy_config_steps s
JOIN legacy_config_zones z ON z.id = s.zone_id
JOIN legacy_config_versions v ON v.id = z.version_id
JOIN (SELECT org_id, max(version) AS version FROM legacy_config_versions GROUP BY org_id) l
  ON l.org_id = v.org_id AND l.version = v.version
JOIN config_regions cr ON cr.org_id = v.org_id AND cr.name = z.name;

DROP TABLE legacy_config_steps;
DROP TABLE legacy_config_zones;
DROP TABLE legacy_config_versions;

-- +goose Down
DROP TABLE config_region_steps;
DROP TABLE config_regions;
DROP TABLE config_profile_steps;
DROP TABLE config_profiles;

-- Recreate the legacy versioned tables (empty — data is not restored).
CREATE TABLE config_profile_versions (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id     BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    version    INT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, version)
);
CREATE TABLE config_zones (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    version_id BIGINT NOT NULL REFERENCES config_profile_versions(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    priority   INT NOT NULL DEFAULT 0,
    geofence   JSONB,
    UNIQUE (version_id, name)
);
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
