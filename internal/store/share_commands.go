package store

import (
	"context"
	"fmt"
)

// CanSendCommand reports whether userID may send the catalog command commandID
// to repeaterID. Allowed if any of:
//   - they own the repeater (any command), or
//   - a share grants them that specific command, or
//   - the repeater is contributed to an org they're a member of, and the command
//     is in BOTH the consented and the current policy version for their effective
//     tier (member → member tier; admin → member OR admin tier). This implements
//     effective = consented ∩ current, with admins ⊇ members.
func (s *Store) CanSendCommand(ctx context.Context, userID, repeaterID, commandID int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT
		    EXISTS (SELECT 1 FROM repeaters WHERE id = $2 AND owner_id = $1)
		 OR EXISTS (SELECT 1 FROM share_commands WHERE repeater_id = $2 AND user_id = $1 AND command_id = $3)
		 OR EXISTS (
		      SELECT 1
		      FROM org_repeaters orp
		      JOIN org_members om ON om.org_id = orp.org_id AND om.user_id = $1
		      JOIN org_permission_commands consented
		           ON consented.version_id = orp.consented_version_id
		          AND consented.command_id = $3
		          AND (consented.tier = 'member' OR (om.role = 'admin' AND consented.tier = 'admin'))
		      JOIN org_permission_versions cur
		           ON cur.org_id = orp.org_id
		          AND cur.version = (SELECT max(version) FROM org_permission_versions WHERE org_id = orp.org_id)
		      JOIN org_permission_commands current
		           ON current.version_id = cur.id
		          AND current.command_id = $3
		          AND (current.tier = 'member' OR (om.role = 'admin' AND current.tier = 'admin'))
		      WHERE orp.repeater_id = $2
		 )`,
		userID, repeaterID, commandID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("can send command: %w", err)
	}
	return ok, nil
}

// ListShareCommandIDs returns the command ids granted to a shared user on a repeater.
func (s *Store) ListShareCommandIDs(ctx context.Context, repeaterID, userID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT command_id FROM share_commands WHERE repeater_id = $1 AND user_id = $2`,
		repeaterID, userID)
	if err != nil {
		return nil, fmt.Errorf("list share commands: %w", err)
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

// SetShareCommands replaces the command grants for a shared user on a repeater.
func (s *Store) SetShareCommands(ctx context.Context, repeaterID, userID int64, commandIDs []int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`DELETE FROM share_commands WHERE repeater_id = $1 AND user_id = $2`, repeaterID, userID); err != nil {
		return fmt.Errorf("clear share commands: %w", err)
	}
	for _, id := range commandIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO share_commands (repeater_id, user_id, command_id) VALUES ($1, $2, $3)
			 ON CONFLICT DO NOTHING`, repeaterID, userID, id); err != nil {
			return fmt.Errorf("insert share command: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// SeedShareCommands grants the share-default command set to a newly shared user,
// without disturbing any existing grants.
func (s *Store) SeedShareCommands(ctx context.Context, repeaterID, userID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO share_commands (repeater_id, user_id, command_id)
		SELECT $1, $2, id FROM command_catalog WHERE in_share_default
		ON CONFLICT DO NOTHING`, repeaterID, userID)
	if err != nil {
		return fmt.Errorf("seed share commands: %w", err)
	}
	return nil
}
