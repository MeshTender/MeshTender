package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CanSendCommand reports whether userID may send the catalog command commandID
// to repeaterID. Allowed if any of:
//   - they own the repeater (any command), or
//   - a share grants them that specific command, or
//   - the repeater participates in an org they and the owner both belong to (the
//     owner is a member and hasn't excluded the repeater), the command is within
//     the site ceiling for their tier (member → org_member_allowed; admin →
//     org_member_allowed OR org_admin_allowed), AND the owner either set no opt-in
//     list for that org (permissive) or listed this command.
func (s *Store) CanSendCommand(ctx context.Context, userID, repeaterID, commandID int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT
		    EXISTS (SELECT 1 FROM repeaters WHERE id = $2 AND owner_id = $1)
		 OR EXISTS (SELECT 1 FROM share_commands WHERE repeater_id = $2 AND user_id = $1 AND command_id = $3)
		 OR EXISTS (
		      SELECT 1
		      FROM repeaters r
		      JOIN org_members ownm ON ownm.user_id = r.owner_id              -- owner's org memberships
		      JOIN org_members usrm ON usrm.org_id = ownm.org_id AND usrm.user_id = $1
		      JOIN command_catalog c ON c.id = $3
		      WHERE r.id = $2
		        AND NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
		                        WHERE e.org_id = ownm.org_id AND e.repeater_id = r.id)
		        AND (c.org_member_allowed OR (usrm.role = 'admin' AND c.org_admin_allowed))
		        AND (
		              NOT EXISTS (SELECT 1 FROM org_command_optin o
		                          WHERE o.org_id = ownm.org_id AND o.owner_id = r.owner_id)
		           OR EXISTS (SELECT 1 FROM org_command_optin o
		                      WHERE o.org_id = ownm.org_id AND o.owner_id = r.owner_id AND o.command_id = $3)
		            )
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
	return collectRows(rows, scanID)
}

// SetShareCommands replaces the command grants for a shared user on a repeater.
func (s *Store) SetShareCommands(ctx context.Context, repeaterID, userID int64, commandIDs []int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
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
		return nil
	})
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
