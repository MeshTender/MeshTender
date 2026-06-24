package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Maintenance history is the manual sibling of the command log: human-entered
// records of physical service work (antenna swap, battery replacement, site
// visit) that would otherwise live only in the builder's head.

// MaintenanceEntry is one logged maintenance record. AuthorName is denormalized
// so the record stays readable after the author leaves (author_id goes NULL).
type MaintenanceEntry struct {
	ID          int64
	AuthorID    *int64
	AuthorName  string
	Note        string
	PerformedAt time.Time
	CreatedAt   time.Time
}

// AddMaintenanceEntry records a maintenance note for a repeater. authorName is
// captured at write time so the entry survives the author's account being deleted.
func (s *Store) AddMaintenanceEntry(ctx context.Context, repeaterID, authorID int64, authorName, note string, performedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO repeater_maintenance (repeater_id, author_id, author_name, note, performed_at)
		VALUES ($1, $2, $3, $4, $5)`,
		repeaterID, authorID, authorName, note, performedAt)
	if err != nil {
		return fmt.Errorf("add maintenance entry: %w", err)
	}
	return nil
}

// ListMaintenance returns a repeater's maintenance history, most recent first.
func (s *Store) ListMaintenance(ctx context.Context, repeaterID int64) ([]MaintenanceEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, author_id, author_name, note, performed_at, created_at
		FROM repeater_maintenance
		WHERE repeater_id = $1
		ORDER BY performed_at DESC, id DESC`, repeaterID)
	if err != nil {
		return nil, fmt.Errorf("list maintenance: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (MaintenanceEntry, error) {
		var e MaintenanceEntry
		err := r.Scan(&e.ID, &e.AuthorID, &e.AuthorName, &e.Note, &e.PerformedAt, &e.CreatedAt)
		return e, err
	})
}

// DeleteMaintenanceEntry removes one entry, scoped to its repeater (so the
// owner-only handler can only delete entries for that repeater). Idempotent.
func (s *Store) DeleteMaintenanceEntry(ctx context.Context, repeaterID, entryID int64) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM repeater_maintenance WHERE id = $1 AND repeater_id = $2`, entryID, repeaterID)
	if err != nil {
		return fmt.Errorf("delete maintenance entry: %w", err)
	}
	return nil
}
