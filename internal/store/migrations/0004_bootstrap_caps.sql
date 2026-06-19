-- +goose Up
-- Upgrade bootstrap: when an existing instance gains capability columns but no
-- one yet holds them, promote the earliest account to instance superadmin so
-- the instance isn't left unmanageable (and so a random new signup isn't the
-- one auto-promoted by the CreateUser bootstrap). Fresh installs have no users
-- yet, so this is a no-op there and CreateUser handles the first signup.
UPDATE users SET cap_manage_users = TRUE, cap_manage_catalog = TRUE
WHERE id = (SELECT min(id) FROM users)
  AND NOT EXISTS (SELECT 1 FROM users WHERE cap_manage_users);

-- +goose Down
SELECT 1;
