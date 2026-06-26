-- +goose Up
-- Regions become a named hierarchy compiled into MeshCore `region def` chains,
-- not geofenced bags of command steps. A repeater's location selects every
-- region whose polygon contains it (plus match-all regions); sorted by layer,
-- their short names (tokens) form `region def <tokens>`. The old step-based
-- regions don't map onto this model, so the table is rebuilt empty — orgs
-- re-author their region tree in the new map editor.
DROP TABLE config_region_steps;
DROP TABLE config_regions;

CREATE TABLE config_regions (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id       BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token        TEXT NOT NULL,          -- MeshCore region name, e.g. "buf"
    display_name TEXT NOT NULL,          -- human label, e.g. "Buffalo"
    layer        INT NOT NULL DEFAULT 0, -- depth / region def order: lower = nearer the root
    geofence     JSONB,                  -- GeoJSON Polygon/MultiPolygon; NULL = everywhere
    UNIQUE (org_id, token)
);

-- +goose Down
DROP TABLE config_regions;

CREATE TABLE config_regions (
    id        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id    BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name      TEXT NOT NULL,
    priority  INT NOT NULL DEFAULT 0,
    geofence  JSONB,
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
