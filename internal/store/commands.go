package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Command is a row in the firmware command catalog.
type Command struct {
	ID       int64
	Key      string
	Template string
	Category string
	Args     string
	// Arity is the exact number of whitespace-separated argument tokens the
	// command takes (after its command token). -1 means variadic / rest-of-line
	// (e.g. "set name <text>"). The console parser authorizes by (token, arity).
	Arity       int
	Description string
	// Feature is the grouping area (e.g. "Radio", "Region") and Operation is the
	// read/write/delete/action bucket, both used by the review/catalog UIs.
	Feature   string
	Operation string
	Risky     bool
	// InShareDefault seeds the command set offered for a new one-off share.
	InShareDefault bool
	// OrgMemberAllowed / OrgAdminAllowed are the site-admin-controlled ceiling of
	// what an org member / admin may ever run on a contributed repeater. (No longer
	// just a seed — these are the authoritative per-tier limits.)
	OrgMemberAllowed bool
	OrgAdminAllowed  bool
}

const commandCols = `id, key, template, category, args, arity, description, feature, operation, risky,
	in_share_default, org_member_allowed, org_admin_allowed`

func scanCommand(row pgx.Row) (*Command, error) {
	var c Command
	err := row.Scan(&c.ID, &c.Key, &c.Template, &c.Category, &c.Args, &c.Arity, &c.Description,
		&c.Feature, &c.Operation, &c.Risky,
		&c.InShareDefault, &c.OrgMemberAllowed, &c.OrgAdminAllowed)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListCommands returns the whole catalog ordered by category then template.
func (s *Store) ListCommands(ctx context.Context) ([]*Command, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+commandCols+` FROM command_catalog ORDER BY category, template`)
	if err != nil {
		return nil, fmt.Errorf("list commands: %w", err)
	}
	return collectRows(rows, scanCommand)
}

// UpdateCommandFlags updates the catalog metadata an instance-admin controls.
func (s *Store) UpdateCommandFlags(ctx context.Context, id int64, risky, share, orgMember, orgAdmin bool) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE command_catalog
		SET risky = $2, in_share_default = $3, org_member_allowed = $4, org_admin_allowed = $5
		WHERE id = $1`, id, risky, share, orgMember, orgAdmin)
	if err != nil {
		return fmt.Errorf("update command flags: %w", err)
	}
	return nil
}
