package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Ownership transfer hands a repeater to one of its stewards. Stewards are
// already the designated co-maintainers — they run every command the owner can
// and are listed on the public page as who to call — so promoting one to owner
// is the smallest step that lets a site outlive the person who built it, and it
// is the alternative to deleting a node when its owner leaves.
//
// Only a steward can receive a repeater. That keeps the recipient set to people
// the owner has already vouched for and who already have access, rather than any
// account on the instance.

// ErrNotSteward is returned when a transfer names someone who does not currently
// hold a steward share on the repeater — a stale form (they were demoted while
// the page was open), or a hand-crafted request.
var ErrNotSteward = errors.New("store: recipient is not a steward")

// TransferRepeaterToSteward makes newOwnerID the owner of repeaterID, which
// currentOwnerID must currently own and newOwnerID must hold a steward share on.
//
// Everything that makes the node worth keeping moves with it: its public_id (so
// links to its public page keep resolving), documentation, command log,
// confirmations, location, and maintenance history. What changes is who controls
// it:
//
//   - the new owner's own share is dropped (you don't hold a share on your own
//     repeater), along with the per-command grants that went with it;
//   - the outgoing owner becomes a steward — the mirror image of the promotion,
//     so a handover isn't a cliff for the person who built the site. The new
//     owner can remove them;
//   - the outgoing owner's pending share links are deleted: they mint access to
//     a node they no longer control;
//   - the handover is appended to the maintenance history, which is where the
//     next maintainer looks to find out what happened to a node.
//
// Returns ErrNotFound if the repeater doesn't exist or isn't owned by
// currentOwnerID, ErrNotSteward if the recipient isn't a steward, and
// ErrDuplicate if the recipient already registered this same public key under
// their own account (repeaters is UNIQUE on (owner_id, public_key_hex), so there
// is nowhere to put the incoming row).
func (s *Store) TransferRepeaterToSteward(ctx context.Context, currentOwnerID, repeaterID, newOwnerID int64) error {
	if currentOwnerID == newOwnerID {
		// An owner can't be their own steward, so this can only be a malformed
		// request. Reject it before touching the database.
		return ErrNotSteward
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		// Lock the repeater first. Two concurrent transfers serialize here, and the
		// loser re-reads an owner_id that is no longer currentOwnerID and fails,
		// rather than both passing the ownership check and the second silently
		// overwriting the first.
		var ownerID int64
		if err := tx.QueryRow(ctx,
			`SELECT owner_id FROM repeaters WHERE id = $1 FOR UPDATE`, repeaterID).Scan(&ownerID); err != nil {
			return notFoundOr(err, "lock repeater")
		}
		if ownerID != currentOwnerID {
			return ErrNotFound
		}

		// The steward flag is the authorization set, re-checked here rather than
		// trusted from the form: a recipient demoted since the page rendered must
		// not receive the node. Locked too, so a concurrent demotion can't land
		// between this read and the commit.
		var steward bool
		err := tx.QueryRow(ctx,
			`SELECT steward FROM repeater_shares WHERE repeater_id = $1 AND user_id = $2 FOR UPDATE`,
			repeaterID, newOwnerID).Scan(&steward)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotSteward
		}
		if err != nil {
			return fmt.Errorf("check steward: %w", err)
		}
		if !steward {
			return ErrNotSteward
		}

		// Read the outgoing owner's name before the change, for the history entry.
		// AddMaintenanceEntry's convention: the live display name, else username.
		var outgoingName, outgoingHandle, incomingHandle string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(NULLIF(o.display_name, ''), o.username), o.username, n.username
			FROM users o, users n WHERE o.id = $1 AND n.id = $2`,
			currentOwnerID, newOwnerID).Scan(&outgoingName, &outgoingHandle, &incomingHandle); err != nil {
			return fmt.Errorf("load transfer names: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`UPDATE repeaters SET owner_id = $2 WHERE id = $1`, repeaterID, newOwnerID); err != nil {
			if isUniqueViolation(err) {
				return ErrDuplicate
			}
			return fmt.Errorf("transfer repeater: %w", err)
		}

		// The new owner's share is now redundant. Drop its command grants too:
		// share_commands is keyed (repeater_id, user_id, command_id) with no FK to
		// repeater_shares, so it would otherwise outlive the share row and reapply
		// if they were ever shared with again.
		if _, err := tx.Exec(ctx,
			`DELETE FROM share_commands WHERE repeater_id = $1 AND user_id = $2`,
			repeaterID, newOwnerID); err != nil {
			return fmt.Errorf("clear new owner grants: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM repeater_shares WHERE repeater_id = $1 AND user_id = $2`,
			repeaterID, newOwnerID); err != nil {
			return fmt.Errorf("clear new owner share: %w", err)
		}

		// The outgoing owner keeps access as a steward.
		if _, err := tx.Exec(ctx, `
			INSERT INTO repeater_shares (repeater_id, user_id, steward) VALUES ($1, $2, TRUE)
			ON CONFLICT (repeater_id, user_id) DO UPDATE SET steward = TRUE`,
			repeaterID, currentOwnerID); err != nil {
			return fmt.Errorf("keep outgoing owner as steward: %w", err)
		}

		// Pending share links were minted by someone who no longer controls the
		// node; redeeming one after the handover would grant access the new owner
		// never agreed to. (Cascades invite_commands.)
		if _, err := tx.Exec(ctx,
			`DELETE FROM repeater_invites WHERE repeater_id = $1`, repeaterID); err != nil {
			return fmt.Errorf("clear pending share links: %w", err)
		}

		// org_repeater_excludes is the OLD owner's per-org opt-outs. Participation
		// requires the repeater's owner to be a member of the org, so any exclude
		// naming an org the new owner isn't in is now inert — sweep it. Rows for
		// orgs they share stay, so an opt-out that still means something is
		// preserved rather than silently flipped back to visible.
		//
		// (org_command_optin is keyed by owner_id, not repeater_id: it is the old
		// owner's own per-org command ceiling across all their nodes. It stays with
		// them and simply stops applying to this one. Nothing to clean up.)
		if _, err := tx.Exec(ctx, `
			DELETE FROM org_repeater_excludes e
			WHERE e.repeater_id = $1
			  AND NOT EXISTS (SELECT 1 FROM org_members m
			                  WHERE m.org_id = e.org_id AND m.user_id = $2)`,
			repeaterID, newOwnerID); err != nil {
			return fmt.Errorf("sweep stale org excludes: %w", err)
		}

		// Record the handover in the human-readable history. author_id is the
		// outgoing owner (who performed it); author_name is the write-time snapshot
		// that survives their account being deleted — which is exactly the case
		// this feature exists to serve.
		if _, err := tx.Exec(ctx, `
			INSERT INTO repeater_maintenance (repeater_id, author_id, author_name, note)
			VALUES ($1, $2, $3, $4)`,
			repeaterID, currentOwnerID, outgoingName,
			fmt.Sprintf("Ownership transferred from @%s to @%s.", outgoingHandle, incomingHandle)); err != nil {
			return fmt.Errorf("log transfer: %w", err)
		}
		return nil
	})
}
