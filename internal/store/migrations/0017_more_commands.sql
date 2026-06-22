-- +goose Up
-- Two things here:
--   1. Add `arity` + `description` to the command catalog. `arity` is the exact
--      number of whitespace-separated argument tokens a command takes (-1 =
--      variadic / rest-of-line, e.g. "set name <text>"). The console parser
--      authorizes by the exact (command-token, arity) tuple — this is the
--      security boundary, so the arities below must match how the firmware
--      tokenizes (CommonCLI.cpp: whitespace, and commas WITHIN a single token
--      for "set radio"/"tempradio" via parseTextParts(',')).
--   2. Add the commands the 0003 seed missed — setperm and the shared CommonCLI
--      get/set and region subcommands — sourced from docs.meshcore.io and the
--      firmware. Commands the firmware overloads by argument count (setperm,
--      region put/home/default) become separate rows per arity.
--
-- Levels for setperm on a repeater: 0 = guest, 3 = admin (1/2 are room-server
-- only); `setperm <pubkey>` with no level removes the entry.
--
-- Omitted as board/build-specific: bridge.* (RS232/ESP-NOW), pwrmgt.*/
-- bootloader.ver (NRF52 only).

ALTER TABLE command_catalog ADD COLUMN arity       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE command_catalog ADD COLUMN description TEXT    NOT NULL DEFAULT '';

-- Backfill arity + description for the 0003 seed (region and tempradio handled
-- separately below since their template/category also change).
UPDATE command_catalog c SET arity = v.arity, description = v.descr
FROM (VALUES
  ('ver',                  0, 'Returns the firmware version and build date'),
  ('board',                0, 'Shows the board/hardware model'),
  ('clock',                0, 'Shows the current UTC time'),
  ('neighbors',            0, 'Lists recently seen neighbor nodes'),
  ('stats-packets',        0, 'Shows packet counters (serial only)'),
  ('stats-radio',          0, 'Shows radio stats: noise floor, RSSI, SNR, airtime (serial only)'),
  ('stats-core',           0, 'Shows core stats: battery, uptime, queues (serial only)'),
  ('sensor.list',         -1, 'Lists available sensors and values (optional start index)'),
  ('sensor.get',           1, 'Reads a sensor value by key'),
  ('advert',               0, 'Sends a flood advertisement'),
  ('advert.zerohop',       0, 'Sends a zero-hop (local) advertisement'),
  ('clock.sync',           0, 'Syncs the clock to the sender''s time'),
  ('time',                 1, 'Sets the clock to a Unix epoch time'),
  ('reboot',               0, 'Reboots the node'),
  ('clkreboot',            0, 'Resets the clock and reboots'),
  ('clear-stats',          0, 'Resets all statistics counters'),
  ('neighbor.remove',      1, 'Removes neighbors matching a public-key prefix'),
  ('powersaving.on',       0, 'Enables power saving'),
  ('powersaving.off',      0, 'Disables power saving'),
  ('log.start',            0, 'Starts capturing the RX packet log'),
  ('log.stop',             0, 'Stops capturing the RX packet log'),
  ('log.erase',            0, 'Erases the captured packet log'),
  ('gps.on',               0, 'Enables GPS'),
  ('gps.off',              0, 'Disables GPS'),
  ('gps.sync',             0, 'Syncs the clock from GPS'),
  ('gps.setloc',           0, 'Sets the node location from the current GPS fix'),
  ('gps.advert',           0, 'Reads the GPS location advert policy'),
  ('sensor.set',           2, 'Sets a sensor value'),
  ('get.tx',               0, 'Reads the TX power (dBm)'),
  ('get.freq',             0, 'Reads the frequency (MHz)'),
  ('get.radio',            0, 'Reads the radio params (freq, bw, sf, cr)'),
  ('get.name',             0, 'Reads the node name'),
  ('get.repeat',           0, 'Reads the repeat (forwarding) state'),
  ('get.role',             0, 'Reads the node role'),
  ('get.public_key',       0, 'Reads the node public key'),
  ('get.lat',              0, 'Reads the latitude'),
  ('get.lon',              0, 'Reads the longitude'),
  ('get.advert_interval',  0, 'Reads the zero-hop advert interval'),
  ('set.tx',               1, 'Sets the TX power (dBm)'),
  ('set.freq',             1, 'Sets the frequency (MHz); requires reboot'),
  ('set.radio',            1, 'Sets radio params freq,bw,sf,cr (comma-separated); requires reboot'),
  ('set.name',            -1, 'Sets the node name'),
  ('set.repeat',           1, 'Enables/disables packet repeating (on/off)'),
  ('set.lat',              1, 'Sets the latitude (degrees)'),
  ('set.lon',              1, 'Sets the longitude (degrees)'),
  ('set.rxdelay',          1, 'Sets the RX delay base (ms)'),
  ('set.txdelay',          1, 'Sets the flood TX delay factor'),
  ('set.direct_txdelay',   1, 'Sets the direct TX delay factor'),
  ('set.advert_interval',  1, 'Sets the zero-hop advert interval (minutes)'),
  ('set.flood_max',        1, 'Sets the max flood hop count'),
  ('set.flood_max_advert', 1, 'Sets the max advert flood hop count'),
  ('set.flood_max_unscoped',1,'Sets the max unscoped flood hop count'),
  ('set.role',             1, 'Changes the node role (changes node type)'),
  ('set.prv_key',          1, 'Sets the private key (changes identity); requires reboot'),
  ('set.public_key',       1, 'Sets the public key (changes identity)'),
  ('password',            -1, 'Changes the admin password'),
  ('guest.password',      -1, 'Sets the guest/public password'),
  ('erase',                0, 'Factory-resets and wipes the node (serial only)'),
  ('start.ota',            0, 'Starts an over-the-air firmware update'),
  ('poweroff',             0, 'Powers the node off'),
  ('shutdown',             0, 'Powers the node off')
) AS v(key, arity, descr)
WHERE c.key = v.key;

-- tempradio was missing its 5th comma field (timeout minutes); it's one
-- whitespace token (comma-separated), so arity 1.
UPDATE command_catalog
   SET template = 'tempradio <f,bw,sf,cr,mins>', arity = 1,
       description = 'Temporarily applies radio params until reboot (comma-separated)'
 WHERE key = 'tempradio';

-- The bare `region` command lists regions/flood policy — a read, no args.
UPDATE command_catalog
   SET template = 'region', category = 'get', args = 'lists regions/transports', arity = 0,
       description = 'Lists configured regions and flood policy',
       in_share_default = TRUE, in_org_member_default = FALSE, in_org_admin_default = TRUE
 WHERE key = 'region';

INSERT INTO command_catalog (key, template, category, args, arity, description, risky, in_share_default, in_org_member_default, in_org_admin_default) VALUES
  -- Access control & discovery
  ('setperm.set',    'setperm <pubkey> <level>', 'config', '0 guest, 3 admin (1,2 room servers only)', 2, 'Adds or updates an ACL entry for a public key', FALSE, FALSE, FALSE, TRUE),
  ('setperm.remove', 'setperm <pubkey>',         'config', 'pubkey',                                   1, 'Removes the ACL entry for a public key',        FALSE, FALSE, FALSE, TRUE),
  ('get.acl',        'get acl',                  'diag',   'serial only',                               0, 'Lists the access-control list',                 FALSE, FALSE, FALSE, TRUE),
  ('discover.neighbors','discover.neighbors',    'diag',   '',                                          0, 'Broadcasts a zero-hop neighbor discovery',      FALSE, TRUE,  TRUE,  TRUE),

  -- Config set (owner/admin tier)
  ('set.dutycycle',          'set dutycycle <percent>',      'config', '1-100%',           1, 'Sets the duty-cycle limit',                         FALSE, FALSE, FALSE, TRUE),
  ('set.airtime_factor',     'set af <factor>',              'config', 'deprecated',       1, 'Sets the airtime factor (deprecated)',              FALSE, FALSE, FALSE, TRUE),
  ('set.int_thresh',         'set int.thresh <n>',           'config', '',                 1, 'Sets the local interference threshold',             FALSE, FALSE, FALSE, TRUE),
  ('set.cad',                'set cad <on|off>',             'config', '',                 1, 'Enables/disables channel-activity detection',       FALSE, FALSE, FALSE, TRUE),
  ('set.agc_reset_interval', 'set agc.reset.interval <sec>', 'config', 'multiple of 4',    1, 'Sets the AGC reset interval (seconds)',             FALSE, FALSE, FALSE, TRUE),
  ('set.multi_acks',         'set multi.acks <0|1>',         'config', '',                 1, 'Enables/disables multi-ACKs',                       FALSE, FALSE, FALSE, TRUE),
  ('set.allow_read_only',    'set allow.read.only <on|off>', 'config', '',                 1, 'Sets read-only access permission',                  FALSE, FALSE, FALSE, TRUE),
  ('set.flood_advert_interval','set flood.advert.interval <hrs>','config','3-168 hours',   1, 'Sets the flood advert interval',                    FALSE, FALSE, FALSE, TRUE),
  ('set.owner_info',         'set owner.info <text>',        'config', '| for newlines',  -1, 'Sets the owner info text',                          FALSE, FALSE, FALSE, TRUE),
  ('set.path_hash_mode',     'set path.hash.mode <mode>',    'config', '',                 1, 'Sets the advert path-hash size mode',               FALSE, FALSE, FALSE, TRUE),
  ('set.loop_detect',        'set loop.detect <mode>',       'config', 'off|minimal|moderate|strict', 1, 'Sets the loop-detection level',           FALSE, FALSE, FALSE, TRUE),
  ('set.adc_multiplier',     'set adc.multiplier <m>',       'config', '0.0-10.0',         1, 'Sets the battery ADC multiplier',                   FALSE, FALSE, FALSE, TRUE),
  ('powersaving.status',     'powersaving',                  'config', '',                 0, 'Reads the power-saving state',                      FALSE, TRUE,  FALSE, TRUE),
  ('gps.advert.set',         'gps advert <policy>',          'config', 'none|share|prefs', 1, 'Sets the GPS location advert policy',               FALSE, FALSE, FALSE, TRUE),

  -- Radio set (owner/admin tier)
  ('set.radio_rxgain',     'set radio.rxgain <on|off>',     'radio', 'RX boosted gain', 1, 'Sets RX boosted gain',         FALSE, FALSE, FALSE, TRUE),
  ('set.radio_fem_rxgain', 'set radio.fem.rxgain <on|off>', 'radio', 'LoRa FEM RX gain', 1, 'Sets the LoRa FEM RX gain',  FALSE, FALSE, FALSE, TRUE),

  -- Reads (safe to share); guest password reveals a secret, so admin-only
  ('get.dutycycle',          'get dutycycle',           'get', '', 0, 'Reads the duty-cycle limit',          FALSE, TRUE,  FALSE, TRUE),
  ('get.airtime_factor',     'get af',                  'get', '', 0, 'Reads the airtime factor',            FALSE, TRUE,  FALSE, TRUE),
  ('get.int_thresh',         'get int.thresh',          'get', '', 0, 'Reads the interference threshold',    FALSE, TRUE,  FALSE, TRUE),
  ('get.cad',                'get cad',                 'get', '', 0, 'Reads the CAD state',                 FALSE, TRUE,  FALSE, TRUE),
  ('get.agc_reset_interval', 'get agc.reset.interval',  'get', '', 0, 'Reads the AGC reset interval',        FALSE, TRUE,  FALSE, TRUE),
  ('get.multi_acks',         'get multi.acks',          'get', '', 0, 'Reads the multi-ACKs state',          FALSE, TRUE,  FALSE, TRUE),
  ('get.allow_read_only',    'get allow.read.only',     'get', '', 0, 'Reads the read-only access flag',     FALSE, TRUE,  FALSE, TRUE),
  ('get.flood_advert_interval','get flood.advert.interval','get','', 0, 'Reads the flood advert interval',   FALSE, TRUE,  FALSE, TRUE),
  ('get.guest_password',     'get guest.password',      'get', 'reveals the guest password', 0, 'Reads the guest password', FALSE, FALSE, FALSE, TRUE),
  ('get.rxdelay',            'get rxdelay',             'get', '', 0, 'Reads the RX delay base',             FALSE, TRUE,  FALSE, TRUE),
  ('get.txdelay',            'get txdelay',             'get', '', 0, 'Reads the flood TX delay factor',     FALSE, TRUE,  FALSE, TRUE),
  ('get.direct_txdelay',     'get direct.txdelay',      'get', '', 0, 'Reads the direct TX delay factor',    FALSE, TRUE,  FALSE, TRUE),
  ('get.flood_max',          'get flood.max',           'get', '', 0, 'Reads the max flood hop count',       FALSE, TRUE,  FALSE, TRUE),
  ('get.flood_max_advert',   'get flood.max.advert',    'get', '', 0, 'Reads the max advert hop count',      FALSE, TRUE,  FALSE, TRUE),
  ('get.flood_max_unscoped', 'get flood.max.unscoped',  'get', '', 0, 'Reads the max unscoped hop count',    FALSE, TRUE,  FALSE, TRUE),
  ('get.owner_info',         'get owner.info',          'get', '', 0, 'Reads the owner info text',           FALSE, TRUE,  FALSE, TRUE),
  ('get.path_hash_mode',     'get path.hash.mode',      'get', '', 0, 'Reads the path-hash size mode',       FALSE, TRUE,  FALSE, TRUE),
  ('get.loop_detect',        'get loop.detect',         'get', '', 0, 'Reads the loop-detection level',      FALSE, TRUE,  FALSE, TRUE),
  ('get.radio_rxgain',       'get radio.rxgain',        'get', '', 0, 'Reads the RX boosted gain state',     FALSE, TRUE,  FALSE, TRUE),
  ('get.radio_fem_rxgain',   'get radio.fem.rxgain',    'get', '', 0, 'Reads the FEM RX gain state',         FALSE, TRUE,  FALSE, TRUE),
  ('get.adc_multiplier',     'get adc.multiplier',      'get', '', 0, 'Reads the battery ADC multiplier',    FALSE, TRUE,  FALSE, TRUE),

  -- Region subcommands. Reads (get/list/home/default with no arg) are shareable;
  -- the rest mutate region/flood policy, so they're owner/admin-tier.
  ('region.get',         'region get <name>',            'get',    'region details',          1, 'Shows details for a named region',          FALSE, TRUE,  FALSE, TRUE),
  ('region.list',        'region list <allowed|denied>', 'get',    'serial only',             1, 'Lists regions by flood policy',             FALSE, TRUE,  FALSE, TRUE),
  ('region.def',         'region def <tokens>',          'config', 'parent/jump syntax',     -1, 'Defines a region hierarchy in one line',    FALSE, FALSE, FALSE, TRUE),
  ('region.load',        'region load',                  'config', 'interactive or args',    -1, 'Bulk-loads a region hierarchy',             FALSE, FALSE, FALSE, TRUE),
  ('region.save',        'region save',                  'config', '',                        0, 'Persists region changes to storage',        FALSE, FALSE, FALSE, TRUE),
  ('region.allowf',      'region allowf <name>',         'config', 'or * wildcard',           1, 'Enables flooding for a region',             FALSE, FALSE, FALSE, TRUE),
  ('region.denyf',       'region denyf <name>',          'config', 'or * wildcard',           1, 'Disables flooding for a region',            FALSE, FALSE, FALSE, TRUE),
  ('region.put_root',    'region put <name>',            'config', '',                        1, 'Creates a root region',                     FALSE, FALSE, FALSE, TRUE),
  ('region.put_sub',     'region put <name> <parent>',   'config', '',                        2, 'Creates a subregion under a parent',        FALSE, FALSE, FALSE, TRUE),
  ('region.remove',      'region remove <name>',         'config', 'must be empty',           1, 'Removes a named region',                    FALSE, FALSE, FALSE, TRUE),
  ('region.home_get',    'region home',                  'get',    '',                        0, 'Reads the home region',                     FALSE, TRUE,  FALSE, TRUE),
  ('region.home_set',    'region home <name>',           'config', '',                        1, 'Sets the home region',                      FALSE, FALSE, FALSE, TRUE),
  ('region.default_get', 'region default',               'get',    '',                        0, 'Reads the default scope region',            FALSE, TRUE,  FALSE, TRUE),
  ('region.default_set', 'region default <name>',        'config', 'or null',                 1, 'Sets the default scope region',             FALSE, FALSE, FALSE, TRUE);

-- Reclassify risky / share / member / admin defaults across the WHOLE catalog
-- (authoritative — overrides the per-row flags set above and in 0003).
--   risky  = identity changes, the admin password, wipe, OTA, or power-off —
--            things that brick the device or lock the owner out. NOTE: setperm is
--            NOT risky — the owner can always re-authenticate with the password
--            and re-grant access, so the password is the gated secret, not the
--            ACL. Radio settings aren't risky either (a nearby companion fixes them).
--   member = small debug set: read stats/info, send adverts.
--   admin  = mostly trusted: read any non-secret info and set any non-secret
--            config, INCLUDING radio (e.g. retune a shared repeater), ACL changes
--            (setperm), and clkreboot (the only way to wind the clock back).
--   share  = one-off, highly trusted: everything that isn't risky (a superset of
--            admin — also gets the secret commands admins don't).
--
-- Baseline: non-risky, granted to one-off shares and org admins, not org members.
UPDATE command_catalog
   SET risky = FALSE, in_share_default = TRUE,
       in_org_member_default = FALSE, in_org_admin_default = TRUE;
-- Risky: owner-only — excluded from every default tier.
UPDATE command_catalog
   SET risky = TRUE, in_share_default = FALSE, in_org_member_default = FALSE, in_org_admin_default = FALSE
 WHERE key IN ('set.role','set.prv_key','set.public_key','password','erase','start.ota','poweroff','shutdown');
-- Org-member debug set: version/board, clock, neighbors, stats, adverts, discovery.
UPDATE command_catalog
   SET in_org_member_default = TRUE
 WHERE key IN ('ver','board','clock','neighbors','stats-packets','stats-radio','stats-core','advert','advert.zerohop','discover.neighbors');
-- Secret reads/writes: fine for a trusted one-off share, but kept out of the
-- broad org-admin default (so share ends up a strict superset of admin).
UPDATE command_catalog
   SET in_org_admin_default = FALSE
 WHERE key IN ('guest.password','get.guest_password');

-- +goose Down
DELETE FROM command_catalog WHERE key IN (
  'setperm.set', 'setperm.remove', 'get.acl', 'discover.neighbors',
  'set.dutycycle', 'set.airtime_factor', 'set.int_thresh', 'set.cad', 'set.agc_reset_interval',
  'set.multi_acks', 'set.allow_read_only', 'set.flood_advert_interval', 'set.owner_info',
  'set.path_hash_mode', 'set.loop_detect', 'set.adc_multiplier', 'powersaving.status', 'gps.advert.set',
  'set.radio_rxgain', 'set.radio_fem_rxgain',
  'get.dutycycle', 'get.airtime_factor', 'get.int_thresh', 'get.cad', 'get.agc_reset_interval',
  'get.multi_acks', 'get.allow_read_only', 'get.flood_advert_interval', 'get.guest_password',
  'get.rxdelay', 'get.txdelay', 'get.direct_txdelay', 'get.flood_max', 'get.flood_max_advert',
  'get.flood_max_unscoped', 'get.owner_info', 'get.path_hash_mode', 'get.loop_detect',
  'get.radio_rxgain', 'get.radio_fem_rxgain', 'get.adc_multiplier',
  'region.get', 'region.list', 'region.def', 'region.load', 'region.save', 'region.allowf',
  'region.denyf', 'region.put_root', 'region.put_sub', 'region.remove',
  'region.home_get', 'region.home_set', 'region.default_get', 'region.default_set'
);
-- Restore the bare `region` and `tempradio` rows to their 0003 seed state.
UPDATE command_catalog SET template = 'region <...>', category = 'config', args = 'region map ops',
       in_share_default = FALSE, in_org_member_default = FALSE, in_org_admin_default = TRUE
 WHERE key = 'region';
UPDATE command_catalog SET template = 'tempradio <f> <bw> <sf> <cr>' WHERE key = 'tempradio';
ALTER TABLE command_catalog DROP COLUMN description;
ALTER TABLE command_catalog DROP COLUMN arity;
