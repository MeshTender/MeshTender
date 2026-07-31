-- +goose Up
-- Optional account email, plus the single-use emailed tokens that make account
-- recovery possible. Before this, a user who signed up with a password and forgot
-- it was locked out permanently (audit finding B1).
--
-- The address is OPTIONAL and stays that way: signup never asks for one, nothing
-- is gated on having one, and clearing it is one click. Recovery is its only use
-- today; security notifications (a passkey was added, an invite was accepted) are
-- expected later, which is why nothing here is named "recovery_email".
ALTER TABLE users ADD COLUMN email TEXT;

-- NULL means the address is unconfirmed — set but not yet proven to belong to the
-- account. Only a verified address can receive recovery mail, so a typo can't
-- silently redirect someone's reset link (or mail a stranger).
ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;

-- Deliberately NOT unique. Two reasons:
--   1. A unique constraint turns account creation into an email-enumeration
--      oracle ("that address is already in use" tells an attacker who's here).
--   2. One person legitimately runs several accounts on one address — a personal
--      one plus a club/ops one. The reset flow handles the fan-out by listing each
--      account with its own link, so ambiguity is presentational, never a question
--      of which account a token belongs to (a token names exactly one user).
--
-- Partial + functional: the reset lookup is always a case-insensitive match against
-- a VERIFIED address, so the index covers exactly that and stays off the rows
-- (unverified, or no address at all) it would never serve.
CREATE INDEX users_email_lower_verified_idx ON users (lower(email))
    WHERE email IS NOT NULL AND email_verified_at IS NOT NULL;

-- One table for both emailed-token flows. They're the same primitive — a
-- single-use token bound to a user with a TTL — so they share one consume path and
-- one prune sweep rather than duplicating both.
CREATE TABLE email_tokens (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- 'verify' proves an address belongs to the account; 'reset' authorizes setting
    -- a new password. Filtered on at consume time, so a verification link can never
    -- be replayed as a password reset.
    purpose TEXT NOT NULL CHECK (purpose IN ('verify', 'reset')),
    -- The SHA-256 of the token, never the token itself. Unlike repeater_invites
    -- (which stores its token raw), these land in a mailbox and authorize taking
    -- over an account, so a database leak must not hand over live credentials.
    token_hash TEXT NOT NULL UNIQUE,
    -- The address a 'verify' token was issued for, re-checked on redemption so a
    -- link minted for one address can't confirm a different one the user typed
    -- afterwards. NULL for 'reset' (which targets the account, not an address).
    email TEXT,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The janitor sweep deletes by expiry. Redemption deletes the row it consumes, so
-- without this the abandoned ones would accumulate forever.
CREATE INDEX email_tokens_expiry_idx ON email_tokens (expires_at);

-- Per-account throttling counts a user's recent tokens of one purpose, so nobody
-- can be used to flood someone else's inbox (or burn a metered daily send quota).
CREATE INDEX email_tokens_user_purpose_idx ON email_tokens (user_id, purpose, created_at DESC);

-- +goose Down
DROP TABLE email_tokens;
DROP INDEX users_email_lower_verified_idx;
ALTER TABLE users DROP COLUMN email_verified_at;
ALTER TABLE users DROP COLUMN email;
