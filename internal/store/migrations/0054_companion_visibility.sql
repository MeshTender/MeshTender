-- +goose Up
-- A companion can be shown on its owner's public profile (/u/{username}), with a
-- QR to add it as a contact. That replaces the "MeshCore" profile link type, which
-- held the same name + public key, entered separately.
ALTER TABLE companions ADD COLUMN public BOOLEAN NOT NULL DEFAULT FALSE;
-- keep_unconfirmed exempts a companion from the 24-hour cleanup of registrations
-- that are never heard. Only the conversion below sets it: those keys were on
-- people's profiles before companions existed, so they stay until heard (or
-- deleted) rather than vanishing a day after the upgrade.
ALTER TABLE companions ADD COLUMN keep_unconfirmed BOOLEAN NOT NULL DEFAULT FALSE;

-- Convert each MeshCore profile link. A key its owner already registered just
-- becomes public; a key nobody registered becomes a new public companion (up to
-- the 10-per-user cap); a key someone else registered is left to them.
UPDATE companions c SET public = TRUE
FROM user_links l
WHERE l.platform = 'meshcore' AND c.owner_id = l.user_id
  AND c.public_key_hex = lower(btrim(l.url));

INSERT INTO companions (public_id, owner_id, name, public_key_hex, public, keep_unconfirmed)
SELECT substr(replace(gen_random_uuid()::text, '-', ''), 1, 12), n.user_id, n.name, n.key, TRUE, TRUE
FROM (
    SELECT k.*, row_number() OVER (PARTITION BY k.user_id ORDER BY k.position, k.id) AS nth,
           (SELECT count(*) FROM companions c WHERE c.owner_id = k.user_id) AS have
    FROM (
        SELECT DISTINCT ON (lower(btrim(l.url))) l.id, l.user_id, l.position,
               lower(btrim(l.url)) AS key,
               left(COALESCE(NULLIF(btrim(l.label), ''), 'MeshCore companion'), 64) AS name
        FROM user_links l
        WHERE l.platform = 'meshcore' AND lower(btrim(l.url)) ~ '^[0-9a-f]{64}$'
        ORDER BY lower(btrim(l.url)), l.id
    ) k
    WHERE NOT EXISTS (SELECT 1 FROM companions c WHERE c.public_key_hex = k.key)
) n
WHERE n.have + n.nth <= 10;

DELETE FROM user_links WHERE platform = 'meshcore';

-- +goose Down
-- Public companions go back to being MeshCore profile links; the companions stay.
INSERT INTO user_links (user_id, platform, label, url, position)
SELECT owner_id, 'meshcore', name, public_key_hex, 1000 FROM companions WHERE public;
ALTER TABLE companions DROP COLUMN keep_unconfirmed;
ALTER TABLE companions DROP COLUMN public;
