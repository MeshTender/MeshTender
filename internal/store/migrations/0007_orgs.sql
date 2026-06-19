-- +goose Up
CREATE TABLE organizations (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       TEXT NOT NULL,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE org_members (
    org_id    BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role      TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX org_members_user_id_idx ON org_members(user_id);

-- Multi-use, member-only join links; revoke by deleting the row.
CREATE TABLE org_invites (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id      BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token       TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX org_invites_org_id_idx ON org_invites(org_id);

-- Versioned permission policy. Editing publishes a new version; versions are
-- append-only so consent pins stay valid.
CREATE TABLE org_permission_versions (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id     BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    version    INT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, version)
);

CREATE TABLE org_permission_commands (
    version_id BIGINT NOT NULL REFERENCES org_permission_versions(id) ON DELETE CASCADE,
    command_id BIGINT NOT NULL REFERENCES command_catalog(id) ON DELETE CASCADE,
    tier       TEXT NOT NULL CHECK (tier IN ('admin', 'member')),
    PRIMARY KEY (version_id, command_id, tier)
);

-- A repeater contributed to an org, pinned to the permission version its owner
-- consented to.
CREATE TABLE org_repeaters (
    org_id               BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    repeater_id          BIGINT NOT NULL REFERENCES repeaters(id) ON DELETE CASCADE,
    consented_version_id BIGINT NOT NULL REFERENCES org_permission_versions(id),
    contributed_by       BIGINT REFERENCES users(id) ON DELETE SET NULL,
    contributed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, repeater_id)
);
CREATE INDEX org_repeaters_repeater_id_idx ON org_repeaters(repeater_id);

-- +goose Down
DROP TABLE org_repeaters;
DROP TABLE org_permission_commands;
DROP TABLE org_permission_versions;
DROP TABLE org_invites;
DROP TABLE org_members;
DROP TABLE organizations;
