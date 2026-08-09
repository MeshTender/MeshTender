-- +goose Up
-- Classify each recorded request so scanner noise stops being counted as
-- traffic. A single vulnerability scanner can post hundreds of 404s a day under
-- a browser user agent, which previously landed it at the top of "top visitors"
-- and buried the real ones. kind is decided at record time (internal/analytics
-- classify.go): "visit" | "probe" | "notfound" | "bot".
ALTER TABLE analytics_events ADD COLUMN kind TEXT NOT NULL DEFAULT 'visit';
CREATE INDEX analytics_events_kind_ts_idx ON analytics_events (kind, ts);

-- The rollups gain the same dimension so each kind keeps its own daily history
-- and the admin charts can read one kind without scanning raw events.
ALTER TABLE analytics_daily DROP CONSTRAINT analytics_daily_pkey;
ALTER TABLE analytics_daily ADD COLUMN kind TEXT NOT NULL DEFAULT 'visit';
ALTER TABLE analytics_daily ADD PRIMARY KEY (day, kind);

ALTER TABLE analytics_daily_surface DROP CONSTRAINT analytics_daily_surface_pkey;
ALTER TABLE analytics_daily_surface ADD COLUMN kind TEXT NOT NULL DEFAULT 'visit';
ALTER TABLE analytics_daily_surface ADD PRIMARY KEY (day, kind, surface);

ALTER TABLE analytics_daily_path DROP CONSTRAINT analytics_daily_path_pkey;
ALTER TABLE analytics_daily_path ADD COLUMN kind TEXT NOT NULL DEFAULT 'visit';
ALTER TABLE analytics_daily_path ADD PRIMARY KEY (day, kind, path);

-- Backfill history with the same rules the Go classifier applies, so the
-- existing retention window isn't left misreporting scanners as visitors. Bots
-- can't be backfilled — they were dropped before insert and never stored, and
-- no user agent is retained to re-derive them.
-- The gate is "not a 2xx", not "is a 404": a scanner sweeping the www host gets
-- a 301 to the apex, and gating on 404 alone left all of that counted as real
-- traffic. `.json` is absent from the extension list and generic names are
-- matched as whole segments only — /orgs/{id}/repeaters.json,
-- /repeaters/{id}/config.json and /repeaters/{id}/console are real routes.
UPDATE analytics_events SET kind = CASE
    WHEN status NOT BETWEEN 200 AND 299 AND (
        -- an unfilled placeholder from the scanner's own template
        path LIKE '%*%'
        -- generic names that are only suspicious at the root of a host
        OR lower(path) IN ('/api', '/info', '/env', '/server', '/phpinfo', '/console', '/console/',
                           '/config.json', '/config.js', '/aws.config.js',
                           '/server-status', '/server-info', '/v2/_catalog', '/old/')
        -- file types we serve nowhere, in ANY segment: the Laravel Ignition RCE
        -- arrives as /index.php/_ignition/... with the .php mid-path. Editor and
        -- backup droppings may follow the real extension (/phpinfo.php.save,
        -- /phpinfo.php~), so they're allowed to trail the match.
        OR path ~* '\.(php|phps|php[357]|ini|env|ya?ml|sql|py|tfstate|properties|js|aspx?|axd|cgi|jspx?|action|bak|old|swp|save|orig|copy|dist|tmp)(~|\.(save|bak|old|orig|copy|dist|tmp|backup))*($|/)'
        -- other stacks' credential and config files, as whole segments
        OR path ~* '/(firebase-key\.json|credentials\.json|service-account\.json|secrets\.json|settings\.json|appsettings\.json|sftp\.json|package\.json|composer\.json|web\.config|id_rsa|id_dsa|dockerfile|backup\.(zip|tar\.gz))($|/)'
        -- JSON outside the two prefixes where we actually serve it
        OR (path ~* '\.json($|/)' AND path !~ '^/(orgs|repeaters)/')
        -- scanner wordlist fragments: other stacks' panels and RCE entry points
        OR path ~* '(wp-|wordpress|xmlrpc|phpmyadmin|/pma/|adminer|cgi-bin|/vendor/|autodiscover|/owa/|/ecp/|manager/html|/solr/|jenkins|actuator|telescope|eval-stdin|hnap1|graphql|/gql|_profiler|@vite|___proxy_subdomain|debug/default|_catalog|_ignition|webhook-waiting|stats/prometheus|/goform/|/boaform/|_environment|meta-inf)'
        -- we serve no dotfiles; .well-known is the one real convention
        OR (path ~ '/\.[^/]' AND path !~* '/\.well-known($|/)')
    ) THEN 'probe'
    WHEN status = 404 THEN 'notfound'
    ELSE 'visit'
END;

-- Rebuild every rollup from the reclassified raw events. Raw retention (90d)
-- covers the whole window the dashboard can display, so this is a complete
-- rebuild rather than a partial correction.
DELETE FROM analytics_daily;
INSERT INTO analytics_daily (day, kind, requests, visitors)
SELECT ts::date, kind, count(*), count(DISTINCT visitor) FROM analytics_events
GROUP BY ts::date, kind;

DELETE FROM analytics_daily_surface;
INSERT INTO analytics_daily_surface (day, kind, surface, requests)
SELECT ts::date, kind, surface, count(*) FROM analytics_events
GROUP BY ts::date, kind, surface;

DELETE FROM analytics_daily_path;
INSERT INTO analytics_daily_path (day, kind, path, hits)
SELECT ts::date, kind, path, count(*) FROM analytics_events
GROUP BY ts::date, kind, path;

-- +goose Down
DELETE FROM analytics_daily WHERE kind <> 'visit';
DELETE FROM analytics_daily_surface WHERE kind <> 'visit';
DELETE FROM analytics_daily_path WHERE kind <> 'visit';

ALTER TABLE analytics_daily_path DROP CONSTRAINT analytics_daily_path_pkey;
ALTER TABLE analytics_daily_path DROP COLUMN kind;
ALTER TABLE analytics_daily_path ADD PRIMARY KEY (day, path);

ALTER TABLE analytics_daily_surface DROP CONSTRAINT analytics_daily_surface_pkey;
ALTER TABLE analytics_daily_surface DROP COLUMN kind;
ALTER TABLE analytics_daily_surface ADD PRIMARY KEY (day, surface);

ALTER TABLE analytics_daily DROP CONSTRAINT analytics_daily_pkey;
ALTER TABLE analytics_daily DROP COLUMN kind;
ALTER TABLE analytics_daily ADD PRIMARY KEY (day);

DROP INDEX analytics_events_kind_ts_idx;
ALTER TABLE analytics_events DROP COLUMN kind;
