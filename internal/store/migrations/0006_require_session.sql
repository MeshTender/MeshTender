-- +goose Up
-- Sessions are now mandatory for command-log rows. Drop any pre-session rows
-- (nothing is deployed yet) and make session_id required, cascading deletes.
DELETE FROM command_log WHERE session_id IS NULL;
ALTER TABLE command_log DROP CONSTRAINT IF EXISTS command_log_session_id_fkey;
ALTER TABLE command_log
    ADD CONSTRAINT command_log_session_id_fkey
    FOREIGN KEY (session_id) REFERENCES console_sessions(id) ON DELETE CASCADE;
ALTER TABLE command_log ALTER COLUMN session_id SET NOT NULL;

-- +goose Down
ALTER TABLE command_log ALTER COLUMN session_id DROP NOT NULL;
ALTER TABLE command_log DROP CONSTRAINT IF EXISTS command_log_session_id_fkey;
ALTER TABLE command_log
    ADD CONSTRAINT command_log_session_id_fkey
    FOREIGN KEY (session_id) REFERENCES console_sessions(id) ON DELETE SET NULL;
