-- +goose Up
-- What an org recommends its companions and repeaters set beyond the radio.

-- The path hash mode (MeshCore `set path.hash.mode`): 0, 1 or 2 for 1-, 2- or
-- 3-byte path hashes; 3 is reserved by the firmware. NULL until an admin sets
-- it — the firmware default is 0. Profiles can use it as {{ path.hash.mode }}.
ALTER TABLE organizations
    ADD COLUMN path_hash_mode SMALLINT CHECK (path_hash_mode BETWEEN 0 AND 2);

-- The region members set as their companion's default flood scope. Separate
-- from is_primary (which only frames the configuration map): most orgs don't
-- recommend a default scope, and companions then stay unscoped. At most one per
-- org, like the primary region.
ALTER TABLE config_regions ADD COLUMN is_default_scope BOOLEAN NOT NULL DEFAULT false;
CREATE UNIQUE INDEX config_regions_one_default_scope_idx
    ON config_regions (org_id) WHERE is_default_scope;

-- +goose Down
DROP INDEX config_regions_one_default_scope_idx;
ALTER TABLE config_regions DROP COLUMN is_default_scope;
ALTER TABLE organizations DROP COLUMN path_hash_mode;
