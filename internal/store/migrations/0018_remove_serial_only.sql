-- +goose Up
-- These commands are gated to the local serial console in the firmware
-- (CommonCLI.cpp / simple_repeater MyMesh.cpp guard them with
-- `sender_timestamp == 0`), so they never run over the mesh — which is the only
-- way MeshTender sends commands. Remove them so they don't appear in the
-- catalog, console, or permission tiers.
--   stats-packets / stats-radio / stats-core, erase, set freq, set prv.key  (serial only)
--   get acl                                                                 (repeater, serial only)
-- NOTE: get freq, set radio, set public.key and set role are NOT serial-gated,
-- so they stay (retuning over the air uses `set radio <f,bw,sf,cr>`).
DELETE FROM command_catalog WHERE key IN (
  'stats-packets', 'stats-radio', 'stats-core', 'erase', 'set.freq', 'set.prv_key', 'get.acl'
);

-- +goose Down
-- Restore the rows to their post-0017 state (arity/description + reclassified flags).
INSERT INTO command_catalog (key, template, category, args, arity, description, risky, in_share_default, in_org_member_default, in_org_admin_default) VALUES
  ('stats-packets', 'stats-packets',    'diag',   '',                 0, 'Shows packet counters (serial only)',                                FALSE, TRUE,  TRUE,  TRUE),
  ('stats-radio',   'stats-radio',      'diag',   '',                 0, 'Shows radio stats: noise floor, RSSI, SNR, airtime (serial only)',   FALSE, TRUE,  TRUE,  TRUE),
  ('stats-core',    'stats-core',       'diag',   '',                 0, 'Shows core stats: battery, uptime, queues (serial only)',            FALSE, TRUE,  TRUE,  TRUE),
  ('erase',         'erase',            'danger', 'wipes the node',   0, 'Factory-resets and wipes the node (serial only)',                    TRUE,  FALSE, FALSE, FALSE),
  ('set.freq',      'set freq <MHz>',   'radio',  '',                 1, 'Sets the frequency (MHz); requires reboot',                          FALSE, TRUE,  FALSE, TRUE),
  ('set.prv_key',   'set prv.key <hex>','danger', 'changes identity', 1, 'Sets the private key (changes identity); requires reboot',           TRUE,  FALSE, FALSE, FALSE),
  ('get.acl',       'get acl',          'diag',   'serial only',      0, 'Lists the access-control list',                                      FALSE, TRUE,  FALSE, TRUE);
