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
// Note: the users table is preserved here but then pruned — Reset deletes users
// that have no way to sign in (no password and no passkey), which is how seeded
// accounts get cleaned up. See Reset.
var resetPreservedTables = map[string]bool{
	"users":                true,
	"webauthn_credentials": true,
	"sessions":             true,
	"server_identity":      true,
	"command_catalog":      true,
	"goose_db_version":     true,
}

// Reset truncates all application data except the identity/login/catalog tables
// (see resetPreservedTables), then deletes any credential-less users (no password
// and no passkey) — i.e. seeded accounts — so a reset also clears seeded data.
// Users who can actually sign in are kept, and their repeaters still trust the
// server. It's a development convenience; tables are discovered dynamically so new
// migrations are covered automatically. Returns the number of users removed.
func (s *Store) Reset(ctx context.Context) (int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		return 0, fmt.Errorf("reset: list tables: %w", err)
	}
	tables, err := collectRows(rows, func(r pgx.Row) (string, error) {
		var t string
		return t, r.Scan(&t)
	})
	if err != nil {
		return 0, fmt.Errorf("reset: scan tables: %w", err)
	}
	var quoted []string
	for _, t := range tables {
		if !resetPreservedTables[t] {
			quoted = append(quoted, pgx.Identifier{t}.Sanitize())
		}
	}
	if len(quoted) > 0 {
		// RESTART IDENTITY resets sequences; CASCADE handles FK ordering (no preserved
		// table references a wiped one, so CASCADE never reaches the kept tables).
		stmt := "TRUNCATE " + strings.Join(quoted, ", ") + " RESTART IDENTITY CASCADE"
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return 0, fmt.Errorf("reset: truncate: %w", err)
		}
	}
	// Prune accounts that can't sign in: no password and no passkey. This clears
	// seeded users while keeping real ones. Their now-orphaned data is already gone
	// (truncated above), and webauthn_credentials cascades (they have none).
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM users u
		WHERE u.password_hash IS NULL
		  AND NOT EXISTS (SELECT 1 FROM webauthn_credentials c WHERE c.user_id = u.id)`)
	if err != nil {
		return 0, fmt.Errorf("reset: prune credential-less users: %w", err)
	}
	return tag.RowsAffected(), nil
}
