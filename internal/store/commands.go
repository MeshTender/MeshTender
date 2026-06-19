package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Command is a row in the firmware command catalog.
type Command struct {
	ID                 int64
	Key                string
	Template           string
	Category           string
	Args               string
	Risky              bool
	InShareDefault     bool
	InOrgMemberDefault bool
	InOrgAdminDefault  bool
}

const commandCols = `id, key, template, category, args, risky,
	in_share_default, in_org_member_default, in_org_admin_default`

func scanCommand(row pgx.Row) (*Command, error) {
	var c Command
	err := row.Scan(&c.ID, &c.Key, &c.Template, &c.Category, &c.Args, &c.Risky,
		&c.InShareDefault, &c.InOrgMemberDefault, &c.InOrgAdminDefault)
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
	defer rows.Close()
	var out []*Command
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, fmt.Errorf("scan command: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCommand returns a single catalog command by id.
func (s *Store) GetCommand(ctx context.Context, id int64) (*Command, error) {
	c, err := scanCommand(s.pool.QueryRow(ctx, `SELECT `+commandCols+` FROM command_catalog WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get command: %w", err)
	}
	return c, nil
}

// UpdateCommandFlags updates the catalog metadata an instance-admin controls.
func (s *Store) UpdateCommandFlags(ctx context.Context, id int64, risky, share, orgMember, orgAdmin bool) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE command_catalog
		SET risky = $2, in_share_default = $3, in_org_member_default = $4, in_org_admin_default = $5
		WHERE id = $1`, id, risky, share, orgMember, orgAdmin)
	if err != nil {
		return fmt.Errorf("update command flags: %w", err)
	}
	return nil
}

// DefaultShareCommandIDs returns the catalog ids flagged as the share default
// (used to seed a new share's allowed commands).
func (s *Store) DefaultShareCommandIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM command_catalog WHERE in_share_default`)
	if err != nil {
		return nil, fmt.Errorf("default share commands: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
