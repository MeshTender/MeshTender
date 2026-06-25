-- +goose Up
-- Lightweight first-party traffic analytics. analytics_events is the raw, async-
-- written base table (pruned after a retention window); the analytics_daily*
-- tables are periodic rollups the admin screen reads. "visitor" is a
-- daily-rotating salted hash of IP+User-Agent — enough to count distinct people
-- per day without storing any IP or other PII.
CREATE TABLE analytics_events (
    id      BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    ts      TIMESTAMPTZ NOT NULL DEFAULT now(),
    surface TEXT NOT NULL,   -- "root" | "app" | "auth" | "custom"
    host    TEXT NOT NULL,
    path    TEXT NOT NULL,
    method  TEXT NOT NULL,
    status  INT  NOT NULL,
    visitor TEXT NOT NULL    -- daily-rotating hash, not PII
);
CREATE INDEX analytics_events_ts_idx ON analytics_events (ts);

CREATE TABLE analytics_daily (
    day      DATE   NOT NULL PRIMARY KEY,
    requests BIGINT NOT NULL,
    visitors BIGINT NOT NULL
);

CREATE TABLE analytics_daily_surface (
    day      DATE   NOT NULL,
    surface  TEXT   NOT NULL,
    requests BIGINT NOT NULL,
    PRIMARY KEY (day, surface)
);

CREATE TABLE analytics_daily_path (
    day  DATE   NOT NULL,
    path TEXT   NOT NULL,
    hits BIGINT NOT NULL,
    PRIMARY KEY (day, path)
);

-- +goose Down
DROP TABLE analytics_daily_path;
DROP TABLE analytics_daily_surface;
DROP TABLE analytics_daily;
DROP TABLE analytics_events;
