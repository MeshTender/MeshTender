-- +goose Up
-- A console session spans one modem connection; commands are grouped under it.
CREATE TABLE console_sessions (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repeater_id BIGINT NOT NULL REFERENCES repeaters(id) ON DELETE CASCADE,
    user_id     BIGINT REFERENCES users(id) ON DELETE SET NULL,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at    TIMESTAMPTZ
);
CREATE INDEX console_sessions_repeater_id_idx ON console_sessions(repeater_id, started_at DESC);

ALTER TABLE command_log ADD COLUMN session_id BIGINT REFERENCES console_sessions(id) ON DELETE SET NULL;
CREATE INDEX command_log_session_id_idx ON command_log(session_id);

-- +goose Down
ALTER TABLE command_log DROP COLUMN session_id;
DROP TABLE console_sessions;
