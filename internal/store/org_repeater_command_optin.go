package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// A repeater's optional per-org command allowlist. No rows for a (org, repeater)
// pair means permissive — the site ceiling (org_member_allowed / org_admin_allowed)
// applies unchanged. One or more rows restrict that org to exactly the listed
// commands on that repeater (still intersected with the ceiling and the caller's
// tier). Keyed per repeater so one box (e.g. a tower under strict control) can
// diverge from the owner's other repeaters in the same org.

// RepeaterOrgOptInCommandIDs returns the command ids opted into for a repeater in
// an org. An empty result means no restriction (permissive).
func (s *Store) RepeaterOrgOptInCommandIDs(ctx context.Context, orgID, repeaterID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT command_id FROM org_repeater_command_optin WHERE org_id = $1 AND repeater_id = $2`, orgID, repeaterID)
	if err != nil {
		return nil, fmt.Errorf("repeater org opt-in commands: %w", err)
	}
	return collectRows(rows, scanID)
}

// SetRepeaterOrgOptIn replaces a repeater's opt-in command list for an org.
// Passing no ids clears the restriction (reverts to permissive).
func (s *Store) SetRepeaterOrgOptIn(ctx context.Context, orgID, repeaterID int64, commandIDs []int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM org_repeater_command_optin WHERE org_id = $1 AND repeater_id = $2`, orgID, repeaterID); err != nil {
			return fmt.Errorf("clear repeater org opt-in: %w", err)
		}
		for _, id := range commandIDs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO org_repeater_command_optin (org_id, repeater_id, command_id) VALUES ($1, $2, $3)
				 ON CONFLICT DO NOTHING`, orgID, repeaterID, id); err != nil {
				return fmt.Errorf("insert repeater org opt-in: %w", err)
			}
		}
		return nil
	})
}
