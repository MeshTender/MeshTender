-- +goose Up
-- Opaque, non-enumerable identifiers for the resources exposed in URLs. The
-- sequential BIGINT primary keys stay internal (FKs, joins); URLs use these.

-- Repeaters: a random public_id replaces the integer id in URLs.
ALTER TABLE repeaters ADD COLUMN public_id TEXT;
UPDATE repeaters SET public_id = substr(replace(gen_random_uuid()::text, '-', ''), 1, 16)
 WHERE public_id IS NULL;
ALTER TABLE repeaters ALTER COLUMN public_id SET NOT NULL;
CREATE UNIQUE INDEX repeaters_public_id_idx ON repeaters(public_id);

-- Organizations: an admin-chosen slug replaces the integer id in URLs. Backfill
-- existing rows from the name, disambiguating collisions with a numeric suffix.
ALTER TABLE organizations ADD COLUMN slug TEXT;

-- +goose StatementBegin
DO $$
DECLARE
    o        RECORD;
    base     TEXT;
    candidate TEXT;
    n        INT;
BEGIN
    FOR o IN SELECT id, name FROM organizations WHERE slug IS NULL ORDER BY id LOOP
        base := trim(both '-' FROM lower(regexp_replace(o.name, '[^a-z0-9]+', '-', 'gi')));
        IF base = '' THEN
            base := 'org-' || o.id;
        END IF;
        candidate := base;
        n := 1;
        WHILE EXISTS (SELECT 1 FROM organizations WHERE slug = candidate) LOOP
            n := n + 1;
            candidate := base || '-' || n;
        END LOOP;
        UPDATE organizations SET slug = candidate WHERE id = o.id;
    END LOOP;
END $$;
-- +goose StatementEnd

ALTER TABLE organizations ALTER COLUMN slug SET NOT NULL;
CREATE UNIQUE INDEX organizations_slug_idx ON organizations(slug);

-- +goose Down
DROP INDEX organizations_slug_idx;
ALTER TABLE organizations DROP COLUMN slug;
DROP INDEX repeaters_public_id_idx;
ALTER TABLE repeaters DROP COLUMN public_id;
