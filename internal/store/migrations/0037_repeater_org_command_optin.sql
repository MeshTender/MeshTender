-- +goose Up
-- Move the owner's optional per-org command allowlist from per-(owner, org) to
-- per-(repeater, org). The old model forced one restriction across ALL of an
-- owner's repeaters in an org; keying per repeater lets a single box diverge —
-- e.g. a tower repeater under strict control that allows only `advert` while the
-- owner's other repeaters in the same org stay permissive. Semantics are otherwise
-- unchanged: no rows for a (org, repeater) pair = permissive (the site ceiling
-- applies unchanged); >=1 row = restricted to exactly those commands (still
-- intersected with the ceiling and the caller's tier). This is the opposite
-- default from share_commands, where no rows means deny-all.
CREATE TABLE org_repeater_command_optin (
    org_id      BIGINT NOT NULL REFERENCES organizations(id)  ON DELETE CASCADE,
    repeater_id BIGINT NOT NULL REFERENCES repeaters(id)       ON DELETE CASCADE,
    command_id  BIGINT NOT NULL REFERENCES command_catalog(id) ON DELETE CASCADE,
    PRIMARY KEY (org_id, repeater_id, command_id)
);
CREATE INDEX org_repeater_command_optin_repeater_id_idx ON org_repeater_command_optin(repeater_id);

-- Preserve current behavior exactly: replicate each owner's per-org list onto
-- every repeater that owner owns. Repeaters that don't participate in the org
-- (excluded, or owner no longer a member) carry harmless rows the auth query
-- never reaches.
INSERT INTO org_repeater_command_optin (org_id, repeater_id, command_id)
SELECT o.org_id, r.id, o.command_id
FROM org_command_optin o
JOIN repeaters r ON r.owner_id = o.owner_id;

DROP TABLE org_command_optin;

-- +goose Down
-- Best-effort, lossy reverse: collapse per-repeater lists back to per-owner by
-- union. Per-repeater divergence introduced under the new model can't be
-- represented per-owner, so a repeater restricted differently from its siblings
-- widens to the union of the owner's lists for that org on the way down.
CREATE TABLE org_command_optin (
    org_id     BIGINT NOT NULL REFERENCES organizations(id)   ON DELETE CASCADE,
    owner_id   BIGINT NOT NULL REFERENCES users(id)           ON DELETE CASCADE,
    command_id BIGINT NOT NULL REFERENCES command_catalog(id) ON DELETE CASCADE,
    PRIMARY KEY (org_id, owner_id, command_id)
);

INSERT INTO org_command_optin (org_id, owner_id, command_id)
SELECT DISTINCT o.org_id, r.owner_id, o.command_id
FROM org_repeater_command_optin o
JOIN repeaters r ON r.id = o.repeater_id;

DROP TABLE org_repeater_command_optin;
