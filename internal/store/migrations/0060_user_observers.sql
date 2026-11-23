-- +goose Up
-- An observer a member looks after, kept by its public key alone. Everything
-- shown about it (its name, firmware, status, and which organizations it
-- reports to) comes from org_observers rows with the same key. The record
-- controls nothing and proves no ownership: it's a member saying "this one is
-- mine to keep an eye on", added after installing the firmware or by pasting
-- the key. Several members may add the same observer.
CREATE TABLE user_observers (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id      TEXT NOT NULL UNIQUE,
    user_id        BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    public_key_hex TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, public_key_hex)
);

-- +goose Down
DROP TABLE user_observers;
