-- +goose Up
-- What net control's modem session did on the air during a net session.

-- Members acknowledged on the air ("Copied: @[name]"), so an acknowledgement
-- is sent once per session even across modem reconnects or a second net
-- control taking over.
CREATE TABLE net_session_aired (
    session_id BIGINT NOT NULL REFERENCES net_sessions(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    aired_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (session_id, user_id)
);

-- Channel messages the modem heard on the net's channel that start with the
-- net's log prefix. The sender is the name the message claims — channel
-- messages aren't signed, so it's shown as unverified. A message repeated by
-- several repeaters is one row: msg_hash is SHA-256 of the packet payload,
-- which repeaters don't change.
CREATE TABLE net_session_messages (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    session_id BIGINT NOT NULL REFERENCES net_sessions(id) ON DELETE CASCADE,
    msg_hash   BYTEA NOT NULL,
    sender     TEXT NOT NULL,
    body       TEXT NOT NULL,
    heard_at   TIMESTAMPTZ NOT NULL,
    UNIQUE (session_id, msg_hash)
);
CREATE INDEX net_session_messages_session_idx ON net_session_messages (session_id, heard_at, id);

-- +goose Down
DROP TABLE net_session_messages;
DROP TABLE net_session_aired;
