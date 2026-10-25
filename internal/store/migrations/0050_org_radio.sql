-- +goose Up
-- An organization's radio settings: the LoRa parameters its mesh runs on, which
-- config profiles reference through the {{ radio.* }} placeholders instead of
-- hard-coding them (see store.ExpandPlaceholders). Hz-canonical like a
-- repeater's radio_* columns. Optional, but all four or none: a half-set radio
-- can't be turned into a `set radio` command.
ALTER TABLE organizations
    ADD COLUMN radio_freq_hz INTEGER,
    ADD COLUMN radio_bw_hz   INTEGER,
    ADD COLUMN radio_sf      SMALLINT,
    ADD COLUMN radio_cr      SMALLINT,
    ADD CONSTRAINT organizations_radio_all_or_none
        CHECK (num_nulls(radio_freq_hz, radio_bw_hz, radio_sf, radio_cr) IN (0, 4));

-- +goose Down
ALTER TABLE organizations
    DROP CONSTRAINT organizations_radio_all_or_none,
    DROP COLUMN radio_cr,
    DROP COLUMN radio_sf,
    DROP COLUMN radio_bw_hz,
    DROP COLUMN radio_freq_hz;
