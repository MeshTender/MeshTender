-- +goose Up
-- The public org map (ListPublicRepeaterPoints) and public repeater list
-- (ListPublicRepeaters/…Page) join org_members → repeaters(owner_id) and then
-- filter r.show_on_public_org (and, for the map, latitude/longitude NOT NULL).
-- The only pre-existing repeaters index is repeaters_owner_id_idx (all rows), so
-- that filter fell back to a scan of the owner's repeaters. These are served on
-- the unauthenticated root host, so they run on every anonymous visit.
--
-- A partial index on owner_id restricted to the map predicate lets the nested-loop
-- join seek straight to an owner's public, located repeaters — the join key stays
-- owner_id (so the planner can still use it), while the WHERE prunes the index to
-- only the rows the map can show. It also shrinks with the eligible set rather than
-- the whole table.
--
-- Note for a future *instance-wide* map (all orgs, no org_members join): that path
-- would seek by geography, not owner_id, and should get its own (latitude,
-- longitude) index — this one is for the per-org, owner-joined queries.
CREATE INDEX repeaters_public_map_idx ON repeaters (owner_id)
    WHERE show_on_public_org AND latitude IS NOT NULL AND longitude IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS repeaters_public_map_idx;
