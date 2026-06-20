-- +goose Up
-- Org membership is now self-serve: any signed-in user joins from the org page,
-- so the multi-use join-link table is no longer needed.
DROP TABLE org_invites;

-- +goose Down
CREATE TABLE org_invites (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id      BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token       TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX org_invites_org_id_idx ON org_invites(org_id);
