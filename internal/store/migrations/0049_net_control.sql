-- +goose Up
-- Net control: the people who run a net and acknowledge check-ins.

-- A net either treats an observed advert as acknowledged straight away
-- (manual_ack false, the default) or waits for net control to acknowledge each
-- check-in after replying on the air.
ALTER TABLE check_in_nets ADD COLUMN manual_ack BOOLEAN NOT NULL DEFAULT FALSE;

-- Members designated to run a net. Org admins can always act as net control;
-- these are the non-admins who can too. Only effective while still a member.
CREATE TABLE net_controls (
    net_id     BIGINT NOT NULL REFERENCES check_in_nets(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    added_by   BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (net_id, user_id)
);

-- Net control acknowledging a member for one session. If the member's advert
-- was heard, this records that net control replied; if not, it IS the
-- check-in (logged by hand — e.g. they checked in by channel message).
CREATE TABLE net_session_acks (
    session_id BIGINT NOT NULL REFERENCES net_sessions(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    acked_by   BIGINT REFERENCES users(id) ON DELETE SET NULL,
    acked_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    note       TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, user_id)
);

-- +goose Down
DROP TABLE net_session_acks;
DROP TABLE net_controls;
ALTER TABLE check_in_nets DROP COLUMN manual_ack;
