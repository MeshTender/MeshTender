-- +goose Up
-- Admin-issued account recovery: a single-use link that lets someone register a
-- new passkey on their existing account.
--
-- It exists for the case email reset deliberately refuses. A passkey-only account
-- has no password to reset, and email must never be what puts one there. Before
-- this, a passkey-only user whose passkey stopped working was locked out for good.
--
-- The trust anchor is a user manager confirming who's asking, not the mailbox, so
-- only a user manager can mint one. Redeeming it adds a passkey and never a
-- password, so a passkey account stays a passkey account.
--
-- Same table and primitives as 'verify' and 'reset': hashed at rest, single-use,
-- swept by expiry. The email column records the address the link was mailed to,
-- if any, so redeeming it can confirm that address.
ALTER TABLE email_tokens DROP CONSTRAINT email_tokens_purpose_check;
ALTER TABLE email_tokens ADD CONSTRAINT email_tokens_purpose_check
    CHECK (purpose IN ('verify', 'reset', 'recover'));

-- +goose Down
DELETE FROM email_tokens WHERE purpose = 'recover';
ALTER TABLE email_tokens DROP CONSTRAINT email_tokens_purpose_check;
ALTER TABLE email_tokens ADD CONSTRAINT email_tokens_purpose_check
    CHECK (purpose IN ('verify', 'reset'));
