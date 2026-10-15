-- +goose Up
-- Catch the catalog up with MeshCore repeater firmware v1.16.0 → v1.18.0. Most of
-- the radio get/set commands moved out of CommonCLI into CommonRadioPrefs and a
-- per-board MainBoard::handleCommand hook, with the same syntax — so they need no
-- change here. New commands reachable over the mesh:
--   power                       CommonCLI.cpp — battery mV + USB/solar/external/charger state
--   gps diag                    CommonCLI.cpp — GPS provider diagnostics (GPS builds)
--   get/set radio.fem.txgain    board hook (Station G3, Wio Tracker L1 1W), like
--                               radio.fem.rxgain already in the catalog
--
-- Omitted, matching 0017/0018:
--   get/set fan, set fan.lo/.hi  T-Beam 1W board only
--   get/set extra.sf             LR2021 builds only (set is #if USE_LR2021)
--   eth.status                   serial/TCP CLI only (simple_repeater main.cpp), never
--                                reaches the mesh command handler
--
-- Defaults follow the 0017 policy: non-risky reads/writes go to one-off shares and org
-- admins; `power` is a status read like `board`/`clock`, so it joins the org-member
-- debug set too.
INSERT INTO command_catalog (key, template, category, args, arity, description, feature, operation, risky, in_share_default, org_member_allowed, org_admin_allowed) VALUES
  ('power',                'power',                         'diag',  '',               0, 'Shows battery voltage and USB/solar/external/charger power state', 'Power', 'read',  FALSE, TRUE, TRUE,  TRUE),
  ('gps.diag',             'gps diag',                      'diag',  '',               0, 'Shows GPS receiver diagnostics',                                    'GPS',   'read',  FALSE, TRUE, FALSE, TRUE),
  ('get.radio_fem_txgain', 'get radio.fem.txgain',          'get',   '',               0, 'Reads the FEM TX gain state',                                       'Radio', 'read',  FALSE, TRUE, FALSE, TRUE),
  ('set.radio_fem_txgain', 'set radio.fem.txgain <on|off>', 'radio', 'LoRa FEM TX gain', 1, 'Sets the LoRa FEM TX gain',                                       'Radio', 'write', FALSE, TRUE, FALSE, TRUE);

-- The firmware now range-checks these (CommonRadioPrefs.cpp): `set af` rejects
-- values outside 0-9, and `set agc.reset.interval` clamps to 0-1020.
UPDATE command_catalog SET args = '0-9 (deprecated)'           WHERE key = 'set.airtime_factor';
UPDATE command_catalog SET args = '0-1020, multiple of 4'      WHERE key = 'set.agc_reset_interval';

-- +goose Down
UPDATE command_catalog SET args = 'multiple of 4' WHERE key = 'set.agc_reset_interval';
UPDATE command_catalog SET args = 'deprecated'    WHERE key = 'set.airtime_factor';
DELETE FROM command_catalog WHERE key IN ('power', 'gps.diag', 'get.radio_fem_txgain', 'set.radio_fem_txgain');
