-- +goose Up
-- Custom domains an org can CNAME to the app. A domain serves the org's public
-- page once its ownership is proven via a DNS TXT record carrying the token.
CREATE TABLE org_domains (
    id                 BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id             BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    hostname           TEXT NOT NULL UNIQUE,          -- lowercased, no scheme/port
    verification_token TEXT NOT NULL,
    verified_at        TIMESTAMPTZ,                   -- NULL until the DNS TXT check passes
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX org_domains_org_id_idx ON org_domains(org_id);

-- +goose Down
DROP TABLE org_domains;
