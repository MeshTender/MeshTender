-- +goose Up
-- Observers are discovered from their MQTT status reports instead of only
-- entered by hand. A discovered observer is unapproved (approved_at NULL) until
-- an org admin approves it; only approved observers count for check-ins and
-- net logs. The broker authenticates each uplink to its own topic, so the key,
-- reported name and region (the topic's IATA code) can be trusted as the
-- observer's own; approval decides whether it's one of the org's.
ALTER TABLE org_observers
    ADD COLUMN approved_at TIMESTAMPTZ,
    ADD COLUMN approved_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN iata        TEXT NOT NULL DEFAULT '';

-- Every observer registered so far was entered by an admin.
UPDATE org_observers SET approved_at = created_at, approved_by = created_by;

-- +goose Down
DELETE FROM org_observers WHERE approved_at IS NULL;
ALTER TABLE org_observers
    DROP COLUMN iata,
    DROP COLUMN approved_by,
    DROP COLUMN approved_at;
