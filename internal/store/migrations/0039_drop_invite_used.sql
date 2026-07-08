-- +goose Up
-- Single-use links are now deleted the instant they're redeemed (see AcceptInvite):
-- a consumed link is redundant with the recipient who then appears in the
-- People-with-access list, so there's no "used" state to track. Remove links that
-- were already consumed (their recipients already hold a share), then drop the
-- consumed-state columns.
DELETE FROM repeater_invites WHERE used_at IS NOT NULL;
ALTER TABLE repeater_invites DROP COLUMN used_at;
ALTER TABLE repeater_invites DROP COLUMN used_by;

-- +goose Down
ALTER TABLE repeater_invites ADD COLUMN used_at TIMESTAMPTZ;
ALTER TABLE repeater_invites ADD COLUMN used_by BIGINT REFERENCES users(id) ON DELETE SET NULL;
