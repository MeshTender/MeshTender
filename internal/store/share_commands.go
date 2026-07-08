package store

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

// ListSendableCommandIDs returns every catalog command id that userID may send to
// repeaterID. This is the single source of truth for command authorization: both
// the runtime gate (CanSendCommand) and the console's command sidebar
// (core.allowedCommands) derive from it, so the list a user is shown can never
// disagree with what they're actually permitted to run. A command is included if
// any of:
//   - they own the repeater (every command), or
//   - they are a steward of the repeater (every command — a steward is a
//     co-operator with the same command power as the owner), or
//   - a share grants them that specific command, or
//   - the repeater participates in an org they and the owner both belong to (the
//     owner is a member and hasn't excluded the repeater), the command is within
//     the site ceiling for their tier (member → org_member_allowed; admin →
//     org_member_allowed OR org_admin_allowed), AND that repeater has no opt-in
//     list for that org (permissive) or the list includes this command.
func (s *Store) ListSendableCommandIDs(ctx context.Context, userID, repeaterID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id
		FROM command_catalog c
		WHERE
		    EXISTS (SELECT 1 FROM repeaters WHERE id = $2 AND owner_id = $1)
		 OR EXISTS (SELECT 1 FROM repeater_shares WHERE repeater_id = $2 AND user_id = $1 AND steward)
		 OR EXISTS (SELECT 1 FROM share_commands WHERE repeater_id = $2 AND user_id = $1 AND command_id = c.id)
		 OR EXISTS (
		      SELECT 1
		      FROM repeaters r
		      JOIN org_members ownm ON ownm.user_id = r.owner_id              -- owner's org memberships
		      JOIN org_members usrm ON usrm.org_id = ownm.org_id AND usrm.user_id = $1
		      WHERE r.id = $2
		        AND NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
		                        WHERE e.org_id = ownm.org_id AND e.repeater_id = r.id)
		        AND (c.org_member_allowed OR (usrm.role = 'admin' AND c.org_admin_allowed))
		        AND (
		              NOT EXISTS (SELECT 1 FROM org_repeater_command_optin o
		                          WHERE o.org_id = ownm.org_id AND o.repeater_id = r.id)
		           OR EXISTS (SELECT 1 FROM org_repeater_command_optin o
		                      WHERE o.org_id = ownm.org_id AND o.repeater_id = r.id AND o.command_id = c.id)
		            )
		 )`,
		userID, repeaterID)
	if err != nil {
		return nil, fmt.Errorf("list sendable commands: %w", err)
	}
	return collectRows(rows, scanID)
}

// CanSendCommand reports whether userID may send the catalog command commandID to
// repeaterID. It is defined in terms of ListSendableCommandIDs so the per-command
// gate and the sidebar list stay in lockstep (see that method for the rules).
func (s *Store) CanSendCommand(ctx context.Context, userID, repeaterID, commandID int64) (bool, error) {
	ids, err := s.ListSendableCommandIDs(ctx, userID, repeaterID)
	if err != nil {
		return false, err
	}
	return slices.Contains(ids, commandID), nil
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
