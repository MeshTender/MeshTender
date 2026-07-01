-- +goose Up
-- Per-region flood policy and the org's root (*) flood policy. MeshCore controls
-- flooding per region via `region allowf <name>` / `region denyf <name>` (name may
-- be the wildcard root *). Defaults are TRUE so existing configs keep flooding
-- allowed everywhere until an admin opts into scoped flooding (deny at root, allow
-- in defined regions).
ALTER TABLE config_regions ADD COLUMN allow_flood BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE organizations ADD COLUMN root_allow_flood BOOLEAN NOT NULL DEFAULT TRUE;

-- +goose Down
ALTER TABLE organizations DROP COLUMN root_allow_flood;
ALTER TABLE config_regions DROP COLUMN allow_flood;
