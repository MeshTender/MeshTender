-- +goose Up

-- Instance-level capability flags (no fixed tiers).
ALTER TABLE users ADD COLUMN cap_manage_users   BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN cap_manage_catalog BOOLEAN NOT NULL DEFAULT FALSE;

-- Catalog of repeater firmware commands MeshTender can send. There is no global
-- "enabled" flag: a repeater owner may run anything; the default-set flags seed
-- what other people are offered (shares now, orgs in Phase 2). `risky` marks
-- commands that can take/brick a node (kept out of every default).
CREATE TABLE command_catalog (
    id                    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key                   TEXT NOT NULL UNIQUE,   -- stable id, e.g. 'set.tx'
    template              TEXT NOT NULL,          -- how to type it, e.g. 'set tx <0-22>'
    category              TEXT NOT NULL,
    args                  TEXT NOT NULL DEFAULT '', -- human note on arguments
    risky                 BOOLEAN NOT NULL DEFAULT FALSE,
    in_share_default      BOOLEAN NOT NULL DEFAULT FALSE,
    in_org_member_default BOOLEAN NOT NULL DEFAULT FALSE,
    in_org_admin_default  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Commands a specific shared user may run on a specific repeater.
CREATE TABLE share_commands (
    repeater_id BIGINT NOT NULL REFERENCES repeaters(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    command_id  BIGINT NOT NULL REFERENCES command_catalog(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repeater_id, user_id, command_id)
);

-- Audit log: every command send attempt.
CREATE TABLE command_log (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repeater_id  BIGINT NOT NULL REFERENCES repeaters(id) ON DELETE CASCADE,
    user_id      BIGINT REFERENCES users(id) ON DELETE SET NULL,
    command_id   BIGINT REFERENCES command_catalog(id) ON DELETE SET NULL,
    command_text TEXT NOT NULL,
    sent_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    ack_received BOOLEAN NOT NULL DEFAULT FALSE,
    response_text TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX command_log_repeater_id_idx ON command_log(repeater_id, sent_at DESC);

-- Seed the catalog from the MeshCore simple_repeater firmware (CommonCLI.cpp +
-- MyMesh.cpp). Columns: key, template, category, args, risky, share, member, admin.
INSERT INTO command_catalog (key, template, category, args, risky, in_share_default, in_org_member_default, in_org_admin_default) VALUES
  -- diagnostics / read-only
  ('ver',            'ver',                 'diag',   '',              FALSE, TRUE,  FALSE, TRUE),
  ('board',          'board',               'diag',   '',              FALSE, TRUE,  FALSE, TRUE),
  ('clock',          'clock',               'diag',   '',              FALSE, TRUE,  TRUE,  TRUE),
  ('neighbors',      'neighbors',           'diag',   '',              FALSE, TRUE,  TRUE,  TRUE),
  ('stats-packets',  'stats-packets',       'diag',   '',              FALSE, TRUE,  TRUE,  TRUE),
  ('stats-radio',    'stats-radio',         'diag',   '',              FALSE, TRUE,  TRUE,  TRUE),
  ('stats-core',     'stats-core',          'diag',   '',              FALSE, TRUE,  TRUE,  TRUE),
  ('sensor.list',    'sensor list',         'diag',   '',              FALSE, TRUE,  FALSE, TRUE),
  ('sensor.get',     'sensor get <name>',   'diag',   'sensor name',   FALSE, FALSE, FALSE, TRUE),
  -- advertising
  ('advert',         'advert',              'advert', '',              FALSE, TRUE,  TRUE,  TRUE),
  ('advert.zerohop', 'advert.zerohop',      'advert', '',              FALSE, TRUE,  TRUE,  TRUE),
  -- time
  ('clock.sync',     'clock sync',          'time',   '',              FALSE, FALSE, FALSE, TRUE),
  ('time',           'time <epoch>',        'time',   'unix seconds',  FALSE, FALSE, FALSE, TRUE),
  -- operational
  ('reboot',         'reboot',              'power',  '',              FALSE, FALSE, FALSE, TRUE),
  ('clkreboot',      'clkreboot',           'power',  '',              FALSE, FALSE, FALSE, TRUE),
  ('clear-stats',    'clear stats',         'diag',   '',              FALSE, FALSE, FALSE, TRUE),
  ('neighbor.remove','neighbor.remove <hex>','config','pubkey prefix', FALSE, FALSE, FALSE, TRUE),
  ('tempradio',      'tempradio <f> <bw> <sf> <cr>','radio','reverts on reboot', FALSE, FALSE, FALSE, TRUE),
  ('powersaving.on', 'powersaving on',      'config', '',              FALSE, FALSE, FALSE, TRUE),
  ('powersaving.off','powersaving off',     'config', '',              FALSE, FALSE, FALSE, TRUE),
  ('log.start',      'log start',           'diag',   '',              FALSE, FALSE, FALSE, TRUE),
  ('log.stop',       'log stop',            'diag',   '',              FALSE, FALSE, FALSE, TRUE),
  ('log.erase',      'log erase',           'diag',   '',              FALSE, FALSE, FALSE, TRUE),
  ('region',         'region <...>',        'config', 'region map ops',FALSE, FALSE, FALSE, TRUE),
  ('gps.on',         'gps on',              'config', '',              FALSE, FALSE, FALSE, TRUE),
  ('gps.off',        'gps off',             'config', '',              FALSE, FALSE, FALSE, TRUE),
  ('gps.sync',       'gps sync',            'config', '',              FALSE, FALSE, FALSE, TRUE),
  ('gps.setloc',     'gps setloc',          'config', '',              FALSE, FALSE, FALSE, TRUE),
  ('gps.advert',     'gps advert',          'config', '',              FALSE, FALSE, FALSE, TRUE),
  ('sensor.set',     'sensor set <name> <v>','config','',              FALSE, FALSE, FALSE, TRUE),
  -- get.* (read-only config reads): share-default + org-admin
  ('get.tx',         'get tx',              'get',    '',              FALSE, TRUE,  FALSE, TRUE),
  ('get.freq',       'get freq',            'get',    '',              FALSE, TRUE,  FALSE, TRUE),
  ('get.radio',      'get radio',           'get',    '',              FALSE, TRUE,  FALSE, TRUE),
  ('get.name',       'get name',            'get',    '',              FALSE, TRUE,  FALSE, TRUE),
  ('get.repeat',     'get repeat',          'get',    '',              FALSE, TRUE,  FALSE, TRUE),
  ('get.role',       'get role',            'get',    '',              FALSE, TRUE,  FALSE, TRUE),
  ('get.public_key', 'get public.key',      'get',    '',              FALSE, TRUE,  FALSE, TRUE),
  ('get.lat',        'get lat',             'get',    '',              FALSE, TRUE,  FALSE, TRUE),
  ('get.lon',        'get lon',             'get',    '',              FALSE, TRUE,  FALSE, TRUE),
  ('get.advert_interval','get advert.interval','get', '',              FALSE, TRUE,  FALSE, TRUE),
  -- set.* (config changes): org-admin only
  ('set.tx',         'set tx <0-22>',       'radio',  'dBm',           FALSE, FALSE, FALSE, TRUE),
  ('set.freq',       'set freq <MHz>',      'radio',  '',              FALSE, FALSE, FALSE, TRUE),
  ('set.radio',      'set radio <f,bw,sf,cr>','radio','',              FALSE, FALSE, FALSE, TRUE),
  ('set.name',       'set name <text>',     'config', '',              FALSE, FALSE, FALSE, TRUE),
  ('set.repeat',     'set repeat <on|off>', 'config', '',              FALSE, FALSE, FALSE, TRUE),
  ('set.lat',        'set lat <deg>',       'config', '',              FALSE, FALSE, FALSE, TRUE),
  ('set.lon',        'set lon <deg>',       'config', '',              FALSE, FALSE, FALSE, TRUE),
  ('set.rxdelay',    'set rxdelay <ms>',    'config', '',              FALSE, FALSE, FALSE, TRUE),
  ('set.txdelay',    'set txdelay <ms>',    'config', '',              FALSE, FALSE, FALSE, TRUE),
  ('set.direct_txdelay','set direct.txdelay <ms>','config','',         FALSE, FALSE, FALSE, TRUE),
  ('set.advert_interval','set advert.interval <min>','config','',      FALSE, FALSE, FALSE, TRUE),
  ('set.flood_max',  'set flood.max <n>',   'config', '',              FALSE, FALSE, FALSE, TRUE),
  ('set.flood_max_advert','set flood.max.advert <n>','config','',      FALSE, FALSE, FALSE, TRUE),
  ('set.flood_max_unscoped','set flood.max.unscoped <n>','config','',  FALSE, FALSE, FALSE, TRUE),
  -- risky: can take/brick a node — in no default, extra confirmation to grant
  ('set.role',       'set role <type>',     'danger', 'changes node type', TRUE, FALSE, FALSE, FALSE),
  ('set.prv_key',    'set prv.key <hex>',   'danger', 'changes identity',  TRUE, FALSE, FALSE, FALSE),
  ('set.public_key', 'set public.key <hex>','danger', 'changes identity',  TRUE, FALSE, FALSE, FALSE),
  ('password',       'password <pw>',       'danger', 'admin password',    TRUE, FALSE, FALSE, FALSE),
  ('guest.password', 'set guest.password <pw>','danger','guest password',  TRUE, FALSE, FALSE, FALSE),
  ('erase',          'erase',               'danger', 'wipes the node',    TRUE, FALSE, FALSE, FALSE),
  ('start.ota',      'start ota',           'danger', 'firmware update',   TRUE, FALSE, FALSE, FALSE),
  ('poweroff',       'poweroff',            'danger', 'takes node offline',TRUE, FALSE, FALSE, FALSE),
  ('shutdown',       'shutdown',            'danger', 'takes node offline',TRUE, FALSE, FALSE, FALSE);

-- +goose Down
DROP TABLE command_log;
DROP TABLE share_commands;
DROP TABLE command_catalog;
ALTER TABLE users DROP COLUMN cap_manage_catalog;
ALTER TABLE users DROP COLUMN cap_manage_users;
