package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Account deletion. The schema does most of the work — every FK to users either
// cascades or nulls out — so this file is about the two things the schema can't
// decide: what must BLOCK a deletion (leaving an org or the instance with no
// admin), and what must be cleaned up alongside the row.
//
// What deliberately survives, anonymised: the command log keeps its write-time
// sender_username, maintenance entries keep author_name, and orgs/config profiles
// keep their created_by history as NULL. That's by design (see migration 0020) —
// the record of what was done to a repeater outlives the person who did it.

var (
	// ErrSoleOrgAdmin blocks deletion: the user is the only admin of an org that
	// still has other members, which a cascade would leave adminless.
	ErrSoleOrgAdmin = errors.New("store: sole admin of an org with other members")
	// ErrLastSiteAdmin blocks deletion: no one else holds cap_manage_users.
	ErrLastSiteAdmin = errors.New("store: last site administrator")
)

// DeletionOrg is one of the user's organizations, classified by what deleting
// their account would do to it.
type DeletionOrg struct {
	ID      int64
	Slug    string
	Name    string
	Role    string
	Members int
}

// DeletionRepeater is one owned repeater that would be deleted, with the number
// of stewards who could receive it instead (a transfer is the alternative to
// destroying the site's documentation and history).
type DeletionRepeater struct {
	PublicID string
	Name     string
	Stewards int
}

// DeletionPreview is everything the confirm page needs to tell the truth about
// what deletion would do, plus the blockers that would refuse it.
type DeletionPreview struct {
	// Repeaters they own; deleting the account deletes these outright.
	Repeaters []DeletionRepeater
	// OrgsDeleted are orgs where they're the only member — nobody else is left to
	// keep them, so they go with the account.
	OrgsDeleted []DeletionOrg
	// OrgsLeft are orgs that simply lose a member.
	OrgsLeft []DeletionOrg
	// OrgsBlocked are orgs where they're the sole admin but others remain: someone
	// else must be promoted first.
	OrgsBlocked []DeletionOrg
	// LastSiteAdmin is set when no other account holds cap_manage_users.
	LastSiteAdmin bool
	// SharedWithUser counts repeaters other people share with them (access lost,
	// but nothing of anyone else's is destroyed).
	SharedWithUser int
	// Passkeys they have registered.
	Passkeys int
}

// Blocked reports whether deletion would be refused as things stand.
func (p *DeletionPreview) Blocked() bool { return p.LastSiteAdmin || len(p.OrgsBlocked) > 0 }

// orgClassifySQL classifies every org the user belongs to in one pass: the org,
// their role in it, and the member/admin counts that decide whether deleting the
// account would leave it adminless. $1 is the user id.
const orgClassifySQL = `
	SELECT o.id, o.slug, o.name, m.role,
	       (SELECT count(*) FROM org_members x WHERE x.org_id = o.id) AS members,
	       (SELECT count(*) FROM org_members x WHERE x.org_id = o.id AND x.role = 'admin') AS admins
	FROM org_members m
	JOIN organizations o ON o.id = m.org_id
	WHERE m.user_id = $1
	ORDER BY lower(o.name), o.id`

// classifiedOrg is one row of orgClassifySQL.
type classifiedOrg struct {
	DeletionOrg
	Admins int
}

// scanClassifiedOrgs reads orgClassifySQL rows.
func scanClassifiedOrgs(rows pgx.Rows) ([]classifiedOrg, error) {
	return collectRows(rows, func(r pgx.Row) (classifiedOrg, error) {
		var c classifiedOrg
		err := r.Scan(&c.ID, &c.Slug, &c.Name, &c.Role, &c.Members, &c.Admins)
		return c, err
	})
}

// blocksDeletion reports whether this membership stops the account going: the
// user is an admin, the only one, and other people are still in the org.
func (c classifiedOrg) blocksDeletion() bool {
	return c.Role == "admin" && c.Admins <= 1 && c.Members > 1
}

// goesWithAccount reports whether the org should be deleted alongside the
// account: the user is its only member, so nothing of anyone else's is in it.
func (c classifiedOrg) goesWithAccount() bool { return c.Members <= 1 }

// PreviewUserDeletion assembles what deleting userID would do. It is a read-only
// snapshot for the confirm page — DeleteUser re-checks every blocker inside its
// transaction, so a stale preview can't let a blocked deletion through.
func (s *Store) PreviewUserDeletion(ctx context.Context, userID int64) (*DeletionPreview, error) {
	p := &DeletionPreview{}

	rows, err := s.pool.Query(ctx, `
		SELECT r.public_id, r.name,
		       (SELECT count(*) FROM repeater_shares rs
		         WHERE rs.repeater_id = r.id AND rs.steward) AS stewards
		FROM repeaters r WHERE r.owner_id = $1
		ORDER BY lower(r.name), r.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("preview repeaters: %w", err)
	}
	p.Repeaters, err = collectRows(rows, func(r pgx.Row) (DeletionRepeater, error) {
		var d DeletionRepeater
		err := r.Scan(&d.PublicID, &d.Name, &d.Stewards)
		return d, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan preview repeaters: %w", err)
	}

	orgRows, err := s.pool.Query(ctx, orgClassifySQL, userID)
	if err != nil {
		return nil, fmt.Errorf("preview orgs: %w", err)
	}
	orgs, err := scanClassifiedOrgs(orgRows)
	if err != nil {
		return nil, fmt.Errorf("scan preview orgs: %w", err)
	}
	for _, o := range orgs {
		switch {
		case o.blocksDeletion():
			p.OrgsBlocked = append(p.OrgsBlocked, o.DeletionOrg)
		case o.goesWithAccount():
			p.OrgsDeleted = append(p.OrgsDeleted, o.DeletionOrg)
		default:
			p.OrgsLeft = append(p.OrgsLeft, o.DeletionOrg)
		}
	}

	if err := s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT cap_manage_users FROM users WHERE id = $1)
		    AND (SELECT count(*) FROM users WHERE cap_manage_users) <= 1,
		  (SELECT count(*) FROM repeater_shares WHERE user_id = $1),
		  (SELECT count(*) FROM webauthn_credentials WHERE user_id = $1)`,
		userID).Scan(&p.LastSiteAdmin, &p.SharedWithUser, &p.Passkeys); err != nil {
		return nil, fmt.Errorf("preview counts: %w", err)
	}
	return p, nil
}

// DeleteUser permanently deletes an account and everything the schema cascades
// from it: passkeys, logins (which drops every host session at once), profile
// links, org memberships, shares, and the repeaters they own along with those
// repeaters' invites, docs, confirmations, maintenance and command history.
//
// It refuses with ErrLastSiteAdmin or ErrSoleOrgAdmin rather than leaving the
// instance or an organization with nobody able to administer it. Both checks run
// under row locks inside the transaction, so two people deleting simultaneously
// can't both see "someone else is still an admin" and race the count to zero.
//
// Orgs where the user is the only member are deleted with the account — there is
// nobody left to hand them to, and everything in them is the departing user's.
//
// Returns ErrNotFound if the account is already gone.
func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var username string
		var siteAdmin bool
		if err := tx.QueryRow(ctx,
			`SELECT username, cap_manage_users FROM users WHERE id = $1 FOR UPDATE`,
			userID).Scan(&username, &siteAdmin); err != nil {
			return notFoundOr(err, "lock user")
		}

		// Locking every site-admin row serializes concurrent admin deletions: the
		// second one blocks, then re-reads a set that no longer contains the first
		// and correctly finds itself to be the last.
		if siteAdmin {
			rows, err := tx.Query(ctx, `SELECT id FROM users WHERE cap_manage_users FOR UPDATE`)
			if err != nil {
				return fmt.Errorf("lock site admins: %w", err)
			}
			admins, err := collectRows(rows, scanID)
			if err != nil {
				return fmt.Errorf("lock site admins: %w", err)
			}
			if len(admins) <= 1 {
				return ErrLastSiteAdmin
			}
		}

		// Lock the membership rows of every org the user belongs to before
		// classifying them, so a concurrent leave/demote elsewhere can't change the
		// answer between the check and the delete (the same guarantee
		// guardLastAdminTx gives the leave path).
		if _, err := tx.Exec(ctx, `
			SELECT 1 FROM org_members
			WHERE org_id IN (SELECT org_id FROM org_members WHERE user_id = $1)
			FOR UPDATE`, userID); err != nil {
			return fmt.Errorf("lock org memberships: %w", err)
		}
		orgRows, err := tx.Query(ctx, orgClassifySQL, userID)
		if err != nil {
			return fmt.Errorf("classify orgs: %w", err)
		}
		orgs, err := scanClassifiedOrgs(orgRows)
		if err != nil {
			return fmt.Errorf("scan orgs: %w", err)
		}
		var orphaned []int64
		for _, o := range orgs {
			if o.blocksDeletion() {
				return ErrSoleOrgAdmin
			}
			if o.goesWithAccount() {
				orphaned = append(orphaned, o.ID)
			}
		}
		if len(orphaned) > 0 {
			if _, err := tx.Exec(ctx,
				`DELETE FROM organizations WHERE id = ANY($1)`, orphaned); err != nil {
				return fmt.Errorf("delete solo orgs: %w", err)
			}
		}

		// Reserve the freed username for the usual release cooldown. Profiles are
		// public at /u/{username} and @handles are baked into command logs and
		// maintenance notes, so a name freed by deletion must not be claimable the
		// next minute by someone inheriting that history. The row's user_id nulls
		// out with the cascade below, and nameReservedByOther treats NULL as "not
		// you" for every caller — so it's reserved against everyone, which is what
		// a deleted account needs (nobody can prove they were its owner).
		//
		// new_username is empty: this is a release, not a rename to something.
		if _, err := tx.Exec(ctx, `
			INSERT INTO username_changes (user_id, old_username, new_username, changed_by)
			VALUES ($1, $2, '', $1)`, userID, username); err != nil {
			return fmt.Errorf("reserve released username: %w", err)
		}

		if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		return nil
	})
}
