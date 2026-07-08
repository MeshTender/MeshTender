-- +goose Up
-- Record the initial command grant an owner chooses when minting a share link, so
-- AcceptInvite can seed exactly that set instead of the site-wide share default.
-- Deny-by-default like share_commands: no rows = the accepter is granted nothing
-- (the owner picked none), NOT the old default set.
CREATE TABLE invite_commands (
    invite_id  BIGINT NOT NULL REFERENCES repeater_invites(id) ON DELETE CASCADE,
    command_id BIGINT NOT NULL REFERENCES command_catalog(id)  ON DELETE CASCADE,
    PRIMARY KEY (invite_id, command_id)
);

-- Preserve behavior for links already outstanding: before this change they seeded
-- the share-default set on accept, so record that set explicitly for every link
-- that can still be redeemed (used ones are done and need nothing).
INSERT INTO invite_commands (invite_id, command_id)
SELECT i.id, c.id
FROM repeater_invites i
JOIN command_catalog c ON c.in_share_default
WHERE i.used_at IS NULL;

-- +goose Down
DROP TABLE invite_commands;
