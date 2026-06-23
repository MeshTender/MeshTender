-- +goose Up
-- Rework org command permissions away from per-org versioned policies + repeater
-- contribution/consent toward a simpler model:
--   (a) the site catalog flags are the hard per-tier ceiling of what an org may
--       ever run (org_member_allowed / org_admin_allowed, renamed from the old
--       "default" flags that only seeded versions),
--   (b) a repeater participates in every org its owner belongs to unless the
--       owner opts it out (org_repeater_excludes),
--   (c) an owner may optionally restrict, per org, which of the ceiling commands
--       that org may run on their repeaters (org_command_optin); no rows = the
--       full ceiling applies.
-- The versioned policy tables and the consent-pinned contribution table go away.
DROP TABLE org_repeaters;
DROP TABLE org_permission_commands;
DROP TABLE org_permission_versions;

ALTER TABLE command_catalog RENAME COLUMN in_org_member_default TO org_member_allowed;
ALTER TABLE command_catalog RENAME COLUMN in_org_admin_default  TO org_admin_allowed;

-- Opt-out set: a repeater participates in org O iff its owner is a member of O
-- AND there is no exclude row. Absence = included (the common case writes nothing).
CREATE TABLE org_repeater_excludes (
    org_id      BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    repeater_id BIGINT NOT NULL REFERENCES repeaters(id) ON DELETE CASCADE,
    PRIMARY KEY (org_id, repeater_id)
);
CREATE INDEX org_repeater_excludes_repeater_id_idx ON org_repeater_excludes(repeater_id);

-- Per-(owner, org) optional command allowlist. No rows for an (org, owner) pair
-- = permissive (the site ceiling applies); ≥1 row = restricted to exactly those
-- commands. (Note: this is the opposite default from share_commands, where no
-- rows means deny-all.)
CREATE TABLE org_command_optin (
    org_id     BIGINT NOT NULL REFERENCES organizations(id)   ON DELETE CASCADE,
    owner_id   BIGINT NOT NULL REFERENCES users(id)           ON DELETE CASCADE,
    command_id BIGINT NOT NULL REFERENCES command_catalog(id) ON DELETE CASCADE,
    PRIMARY KEY (org_id, owner_id, command_id)
);

-- +goose Down
DROP TABLE org_command_optin;
DROP TABLE org_repeater_excludes;

ALTER TABLE command_catalog RENAME COLUMN org_admin_allowed  TO in_org_admin_default;
ALTER TABLE command_catalog RENAME COLUMN org_member_allowed TO in_org_member_default;

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

CREATE TABLE org_repeaters (
    org_id               BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    repeater_id          BIGINT NOT NULL REFERENCES repeaters(id) ON DELETE CASCADE,
    consented_version_id BIGINT NOT NULL REFERENCES org_permission_versions(id),
    contributed_by       BIGINT REFERENCES users(id) ON DELETE SET NULL,
    contributed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, repeater_id)
);
CREATE INDEX org_repeaters_repeater_id_idx ON org_repeaters(repeater_id);
