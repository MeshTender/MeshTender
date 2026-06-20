-- +goose Up
-- Supports the public directory's keyset pagination: orgs are listed ordered by
-- (name, id), and each page seeks with (name, id) > (cursor). This index lets
-- both the ORDER BY and the seek predicate ride the index instead of sorting the
-- whole table per page.
CREATE INDEX organizations_name_id_idx ON organizations(name, id);

-- +goose Down
DROP INDEX organizations_name_id_idx;
