-- +goose Up
-- Social media and third-party site links shown on an org's public page (e.g. a
-- Discord server, a community wiki). Each link names a known platform (which
-- drives its icon), an optional display label, a URL, and an admin-chosen order.
CREATE TABLE org_links (
    id       BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id   BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    platform TEXT NOT NULL,
    label    TEXT NOT NULL DEFAULT '',
    url      TEXT NOT NULL,
    position INT NOT NULL DEFAULT 0
);
CREATE INDEX org_links_org_idx ON org_links(org_id, position);

-- +goose Down
DROP TABLE org_links;
