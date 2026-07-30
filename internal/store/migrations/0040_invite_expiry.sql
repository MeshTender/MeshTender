-- +goose Up
-- Share links used to be valid forever until redeemed or deleted, so a link pasted
-- into a chat channel or an email years ago still granted repeater access to
-- whoever found it first. Give every link a hard expiry.
--
-- The expiry is STORED per row rather than computed as created_at + a constant, so
-- each link carries the promise made when it was minted. With a computed expiry,
-- editing the constant would retroactively redefine every outstanding link — and
-- lengthening it would resurrect links that had already expired and that their
-- owners had every reason to consider dead. Same instinct as the append-only
-- org_permission_versions table: a commitment made at creation time stays fixed.
ALTER TABLE repeater_invites ADD COLUMN expires_at TIMESTAMPTZ;

-- Backfill outstanding links from their own creation time, not from now(): links
-- older than the window are exactly the stale ones this change exists to kill, so
-- they expire immediately rather than getting a fresh lease.
UPDATE repeater_invites SET expires_at = created_at + interval '7 days';

ALTER TABLE repeater_invites ALTER COLUMN expires_at SET NOT NULL;

-- Supports the expiry predicate on every token lookup, and the pruning sweep.
CREATE INDEX repeater_invites_expires_at_idx ON repeater_invites (expires_at);

-- +goose Down
DROP INDEX repeater_invites_expires_at_idx;
ALTER TABLE repeater_invites DROP COLUMN expires_at;
