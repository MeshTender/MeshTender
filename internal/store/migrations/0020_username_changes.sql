-- +goose Up
-- Usernames become user-changeable. The stable identity is still users.id; the
-- username is a renamable, unique, human-checkable handle. This migration adds
-- the machinery to keep that safe:
--
--  1. username_changes is an admin/security-only audit trail. It maps a
--     historical handle back to a user (forensics after a rename), backs the
--     release cooldown (a freed name is reserved for a window), and records who
--     made each change. Old usernames live here and are never shown publicly.
--  2. sender_username on command_log / console_sessions snapshots the actor's
--     username at write time, so the command log is an immutable, point-in-time
--     record. It no longer depends on the live (renamable, and free-form,
--     spoofable) display name and it survives account deletion.
CREATE TABLE username_changes (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      BIGINT REFERENCES users(id) ON DELETE SET NULL,
    old_username TEXT NOT NULL,
    new_username TEXT NOT NULL,
    changed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    changed_by   BIGINT REFERENCES users(id) ON DELETE SET NULL,
    ip           TEXT,
    user_agent   TEXT
);
-- Forensics: a single user's rename timeline.
CREATE INDEX username_changes_user_id_idx ON username_changes(user_id, changed_at DESC);
-- Cooldown lookup: was this name released recently, and by whom?
CREATE INDEX username_changes_old_username_idx ON username_changes(lower(old_username), changed_at DESC);

ALTER TABLE command_log ADD COLUMN sender_username TEXT;
ALTER TABLE console_sessions ADD COLUMN sender_username TEXT;
-- Backfill existing rows from the current username (the best point-in-time
-- value available retroactively).
UPDATE command_log l SET sender_username = u.username FROM users u WHERE u.id = l.user_id;
UPDATE console_sessions cs SET sender_username = u.username FROM users u WHERE u.id = cs.user_id;

-- +goose Down
ALTER TABLE console_sessions DROP COLUMN sender_username;
ALTER TABLE command_log DROP COLUMN sender_username;
DROP TABLE username_changes;
