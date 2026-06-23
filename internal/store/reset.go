package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// resetPreservedTables are the tables Reset keeps: everything needed to still sign
// in (users + their passkeys + live sessions), the server-wide MeshCore identity
// (so re-added repeaters still trust MeshTender), the command catalog (site config,
// not user data), and goose's migration bookkeeping. Everything else — orgs,
// repeaters, shares, config profiles, logs, console/auth ephemera — is wiped.
var resetPreservedTables = map[string]bool{
	"users":                true,
	"webauthn_credentials": true,
	"sessions":             true,
	"server_identity":      true,
	"command_catalog":      true,
	"goose_db_version":     true,
}

// Reset truncates all application data except the identity/login/catalog tables
// (see resetPreservedTables). It's a development convenience: after a reset you can
// still log in and your repeaters still trust the server, but you start fresh on
// orgs, repeaters, and everything else. Discovers tables dynamically so new
// migrations are covered automatically.
func (s *Store) Reset(ctx context.Context) error {
	rows, err := s.pool.Query(ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		return fmt.Errorf("reset: list tables: %w", err)
	}
	tables, err := collectRows(rows, func(r pgx.Row) (string, error) {
		var t string
		return t, r.Scan(&t)
	})
	if err != nil {
		return fmt.Errorf("reset: scan tables: %w", err)
	}
	var quoted []string
	for _, t := range tables {
		if !resetPreservedTables[t] {
			quoted = append(quoted, pgx.Identifier{t}.Sanitize())
		}
	}
	if len(quoted) == 0 {
		return nil
	}
	// RESTART IDENTITY resets sequences; CASCADE handles FK ordering (no preserved
	// table references a wiped one, so CASCADE never reaches the kept tables).
	stmt := "TRUNCATE " + strings.Join(quoted, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := s.pool.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("reset: truncate: %w", err)
	}
	return nil
}
