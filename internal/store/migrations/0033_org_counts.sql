-- +goose Up
-- Denormalize the public org directory's member/repeater counts onto the
-- organizations row so the directory can sort and keyset-seek on real, indexed
-- columns instead of computing a correlated count subquery per org on every
-- (unauthenticated) page load. Kept exact by triggers that recompute the
-- affected org from source of truth on any membership/repeater/exclude change.
--
--   member_count   = |org_members for the org|
--   repeater_count = repeaters owned by a member of the org, minus per-org
--                    excludes (org_repeater_excludes) — the same definition the
--                    old subquery and OrgCounts use.
ALTER TABLE organizations ADD COLUMN member_count   INT NOT NULL DEFAULT 0;
ALTER TABLE organizations ADD COLUMN repeater_count INT NOT NULL DEFAULT 0;

-- Recompute both counts for one org from the source tables. Recomputing (rather
-- than applying ±1 deltas) is immune to trigger ordering and to the ON DELETE
-- CASCADE on org_repeater_excludes.repeater_id: it always reads the current
-- state. A no-op when the org no longer exists (e.g. recompute racing an org
-- delete cascade) — the UPDATE simply matches no rows.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION org_recompute_counts(p_org_id BIGINT) RETURNS void AS $$
    UPDATE organizations o SET
        member_count = (SELECT count(*) FROM org_members m WHERE m.org_id = o.id),
        repeater_count = (
            SELECT count(*) FROM repeaters r
            JOIN org_members om ON om.org_id = o.id AND om.user_id = r.owner_id
            WHERE NOT EXISTS (
                SELECT 1 FROM org_repeater_excludes e
                WHERE e.org_id = o.id AND e.repeater_id = r.id))
    WHERE o.id = p_org_id;
$$ LANGUAGE sql;
-- +goose StatementEnd

-- A membership change affects exactly one org.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION org_members_recount() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM org_recompute_counts(OLD.org_id);
        RETURN OLD;
    END IF;
    PERFORM org_recompute_counts(NEW.org_id);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- An exclude change affects the repeater_count of exactly one org.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION org_excludes_recount() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM org_recompute_counts(OLD.org_id);
        RETURN OLD;
    END IF;
    PERFORM org_recompute_counts(NEW.org_id);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- A repeater insert/delete/owner-change affects every org its owner(s) belong
-- to. On DELETE the row is already gone when this AFTER trigger runs, so the
-- recompute correctly excludes it; on an owner change both owners' orgs are
-- refreshed.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION repeaters_recount() RETURNS trigger AS $$
DECLARE
    oid BIGINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        FOR oid IN SELECT org_id FROM org_members WHERE user_id = OLD.owner_id LOOP
            PERFORM org_recompute_counts(oid);
        END LOOP;
    END IF;
    IF TG_OP <> 'DELETE' THEN
        FOR oid IN SELECT org_id FROM org_members WHERE user_id = NEW.owner_id LOOP
            PERFORM org_recompute_counts(oid);
        END LOOP;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER org_members_recount_trg
    AFTER INSERT OR DELETE ON org_members
    FOR EACH ROW EXECUTE FUNCTION org_members_recount();

CREATE TRIGGER org_excludes_recount_trg
    AFTER INSERT OR DELETE ON org_repeater_excludes
    FOR EACH ROW EXECUTE FUNCTION org_excludes_recount();

-- UPDATE only matters when the owner changes (name/radio edits don't move a
-- repeater between orgs), so scope the UPDATE event to owner_id.
CREATE TRIGGER repeaters_recount_trg
    AFTER INSERT OR DELETE OR UPDATE OF owner_id ON repeaters
    FOR EACH ROW EXECUTE FUNCTION repeaters_recount();

-- Backfill existing rows now that the triggers are in place.
UPDATE organizations o SET
    member_count = (SELECT count(*) FROM org_members m WHERE m.org_id = o.id),
    repeater_count = (
        SELECT count(*) FROM repeaters r
        JOIN org_members om ON om.org_id = o.id AND om.user_id = r.owner_id
        WHERE NOT EXISTS (
            SELECT 1 FROM org_repeater_excludes e
            WHERE e.org_id = o.id AND e.repeater_id = r.id));

-- Sort/seek indexes for the directory orderings. (Name ASC is already served by
-- organizations_name_id_idx.) Each matches an ORDER BY (col, id) DESC + its
-- keyset row comparison.
CREATE INDEX organizations_member_count_id_idx   ON organizations (member_count DESC, id DESC);
CREATE INDEX organizations_repeater_count_id_idx ON organizations (repeater_count DESC, id DESC);
CREATE INDEX organizations_created_at_id_idx     ON organizations (created_at DESC, id DESC);

-- Trigram indexes so the directory's substring search (name/description/region
-- ILIKE '%q%') stops sequentially scanning the whole table.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX organizations_name_trgm_idx        ON organizations USING gin (name gin_trgm_ops);
CREATE INDEX organizations_description_trgm_idx ON organizations USING gin (description gin_trgm_ops);
CREATE INDEX organizations_region_trgm_idx      ON organizations USING gin (region gin_trgm_ops);

-- +goose Down
DROP INDEX IF EXISTS organizations_region_trgm_idx;
DROP INDEX IF EXISTS organizations_description_trgm_idx;
DROP INDEX IF EXISTS organizations_name_trgm_idx;
DROP EXTENSION IF EXISTS pg_trgm;
DROP INDEX IF EXISTS organizations_created_at_id_idx;
DROP INDEX IF EXISTS organizations_repeater_count_id_idx;
DROP INDEX IF EXISTS organizations_member_count_id_idx;
DROP TRIGGER IF EXISTS repeaters_recount_trg ON repeaters;
DROP TRIGGER IF EXISTS org_excludes_recount_trg ON org_repeater_excludes;
DROP TRIGGER IF EXISTS org_members_recount_trg ON org_members;
DROP FUNCTION IF EXISTS repeaters_recount();
DROP FUNCTION IF EXISTS org_excludes_recount();
DROP FUNCTION IF EXISTS org_members_recount();
DROP FUNCTION IF EXISTS org_recompute_counts(BIGINT);
ALTER TABLE organizations DROP COLUMN repeater_count;
ALTER TABLE organizations DROP COLUMN member_count;
