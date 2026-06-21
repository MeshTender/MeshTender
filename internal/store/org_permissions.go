package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CurrentVersion returns the org's latest permission version (id and number).
func (s *Store) CurrentVersion(ctx context.Context, orgID int64) (id int64, version int, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT id, version FROM org_permission_versions WHERE org_id = $1 ORDER BY version DESC LIMIT 1`,
		orgID).Scan(&id, &version)
	if err != nil {
		return 0, 0, notFoundOr(err, "current version")
	}
	return id, version, nil
}

// VersionCommandIDs returns the command ids in a version, split by tier.
func (s *Store) VersionCommandIDs(ctx context.Context, versionID int64) (admin, member []int64, err error) {
	rows, err := s.pool.Query(ctx,
		`SELECT command_id, tier FROM org_permission_commands WHERE version_id = $1`, versionID)
	if err != nil {
		return nil, nil, fmt.Errorf("version commands: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var tier string
		if err := rows.Scan(&id, &tier); err != nil {
			return nil, nil, err
		}
		if tier == "admin" {
			admin = append(admin, id)
		} else {
			member = append(member, id)
		}
	}
	return admin, member, rows.Err()
}

// VersionNumber returns the version number for a permission version id.
func (s *Store) VersionNumber(ctx context.Context, versionID int64) (int, error) {
	var v int
	err := s.pool.QueryRow(ctx,
		`SELECT version FROM org_permission_versions WHERE id = $1`, versionID).Scan(&v)
	if err != nil {
		return 0, notFoundOr(err, "version number")
	}
	return v, nil
}

// VersionNote is a changelog entry.
type VersionNote struct {
	Version int
	Note    string
}

// VersionNotesSince returns the notes for org versions newer than afterVersion,
// oldest first — the changelog an owner reviews before re-consenting.
func (s *Store) VersionNotesSince(ctx context.Context, orgID int64, afterVersion int) ([]VersionNote, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT version, note FROM org_permission_versions
		 WHERE org_id = $1 AND version > $2 ORDER BY version`, orgID, afterVersion)
	if err != nil {
		return nil, fmt.Errorf("version notes: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (VersionNote, error) {
		var n VersionNote
		err := r.Scan(&n.Version, &n.Note)
		return n, err
	})
}

// PublishVersion creates the org's next permission version with the given
// admin/member command sets, returning the new version number.
func (s *Store) PublishVersion(ctx context.Context, orgID int64, note string, createdBy int64, adminIDs, memberIDs []int64) (int, error) {
	var next int
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(max(version), 0) + 1 FROM org_permission_versions WHERE org_id = $1`,
			orgID).Scan(&next); err != nil {
			return fmt.Errorf("next version: %w", err)
		}
		var versionID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO org_permission_versions (org_id, version, note, created_by)
			 VALUES ($1, $2, $3, $4) RETURNING id`,
			orgID, next, note, createdBy).Scan(&versionID); err != nil {
			return fmt.Errorf("insert version: %w", err)
		}
		insert := func(ids []int64, tier string) error {
			for _, id := range ids {
				if _, err := tx.Exec(ctx,
					`INSERT INTO org_permission_commands (version_id, command_id, tier) VALUES ($1, $2, $3)
					 ON CONFLICT DO NOTHING`, versionID, id, tier); err != nil {
					return err
				}
			}
			return nil
		}
		if err := insert(adminIDs, "admin"); err != nil {
			return fmt.Errorf("insert admin commands: %w", err)
		}
		if err := insert(memberIDs, "member"); err != nil {
			return fmt.Errorf("insert member commands: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}
