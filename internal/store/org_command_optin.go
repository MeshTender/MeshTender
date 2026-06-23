package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// An owner's optional per-org command allowlist. No rows for an (org, owner) pair
// means permissive — the site ceiling (org_member_allowed / org_admin_allowed)
// applies unchanged. One or more rows restrict that org to exactly the listed
// commands on the owner's repeaters (still intersected with the ceiling and tier).

// OrgOptInCommandIDs returns the command ids an owner has opted into for an org.
// An empty result means no restriction (permissive).
func (s *Store) OrgOptInCommandIDs(ctx context.Context, orgID, ownerID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT command_id FROM org_command_optin WHERE org_id = $1 AND owner_id = $2`, orgID, ownerID)
	if err != nil {
		return nil, fmt.Errorf("org opt-in commands: %w", err)
	}
	return collectRows(rows, scanID)
}

// SetOrgOptIn replaces an owner's opt-in command list for an org. Passing no ids
// clears the restriction (reverts to permissive).
func (s *Store) SetOrgOptIn(ctx context.Context, orgID, ownerID int64, commandIDs []int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM org_command_optin WHERE org_id = $1 AND owner_id = $2`, orgID, ownerID); err != nil {
			return fmt.Errorf("clear org opt-in: %w", err)
		}
		for _, id := range commandIDs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO org_command_optin (org_id, owner_id, command_id) VALUES ($1, $2, $3)
				 ON CONFLICT DO NOTHING`, orgID, ownerID, id); err != nil {
				return fmt.Errorf("insert org opt-in: %w", err)
			}
		}
		return nil
	})
}
