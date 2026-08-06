-- +goose Up
-- Regions are edited one at a time now (per-region CRUD) rather than replaced as a
-- whole set, so "at most one primary region per org" can no longer be enforced by
-- the editor's JavaScript clearing the other switches. CreateRegion/UpdateRegion
-- clear any other primary in the same transaction; this index is the backstop that
-- keeps the invariant true no matter who writes the table.
--
-- Existing data can hold more than one primary (the old bulk editor only enforced
-- exclusivity client-side), so demote all but the lowest-id primary per org before
-- the unique index goes on.
UPDATE config_regions SET is_primary = false
WHERE is_primary
  AND id NOT IN (SELECT min(id) FROM config_regions WHERE is_primary GROUP BY org_id);

CREATE UNIQUE INDEX config_regions_one_primary_idx
    ON config_regions (org_id) WHERE is_primary;

-- +goose Down
DROP INDEX config_regions_one_primary_idx;
