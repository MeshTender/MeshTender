package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// A user observer is an observer a member keeps an eye on, recorded by its
// public key alone (migration 0060). What MeshTender knows about it comes from
// the org_observers rows with that key: each organization whose broker it
// reports to has one.

// MaxObserversPerUser bounds a member's observer list, and with it every
// per-user observer query.
const MaxObserversPerUser = 20

// maxObserverReports caps the organizations listed for one observer. An
// observer on a busy public broker can be discovered by every org listening to
// it; past this, the page lists the most recently heard.
const maxObserverReports = 50

// ErrObserverLimit is returned when the user already has MaxObserversPerUser.
var ErrObserverLimit = errors.New("observer limit reached")

// UserObserver is an observer in a member's list, with the status its most
// recently heard organization recorded for it. The status fields are zero
// until some organization's broker has heard from it.
type UserObserver struct {
	ID           int64
	PublicID     string
	UserID       int64
	PublicKeyHex string
	CreatedAt    time.Time

	// From the org_observers row heard most recently. Name is the name that
	// organization lists it under: the observer's own until an admin approves
	// it under another.
	Name            string
	FirmwareVersion string
	ClientVersion   string
	Model           string
	IATA            string
	LastSeenAt      *time.Time
	BatteryMV       *int
	NoiseFloor      *int

	// OrgCount is how many organizations have it in their observer list.
	OrgCount int
}

// Reported reports whether any organization has heard from it or listed it.
func (o *UserObserver) Reported() bool { return o.OrgCount > 0 }

const userObserverSelect = `
	SELECT uo.id, uo.public_id, uo.user_id, uo.public_key_hex, uo.created_at,
	       COALESCE(latest.name, ''), COALESCE(latest.firmware_version, ''), COALESCE(latest.client_version, ''),
	       COALESCE(latest.model, ''), COALESCE(latest.iata, ''), latest.last_seen_at, latest.battery_mv, latest.noise_floor,
	       (SELECT count(*) FROM org_observers c WHERE c.public_key_hex = uo.public_key_hex)
	FROM user_observers uo
	LEFT JOIN LATERAL (
		SELECT o.name, o.firmware_version, o.client_version, o.model, o.iata, o.last_seen_at, o.battery_mv, o.noise_floor
		FROM org_observers o
		WHERE o.public_key_hex = uo.public_key_hex
		ORDER BY o.last_seen_at DESC NULLS LAST, o.id
		LIMIT 1
	) latest ON true`

func scanUserObserver(row pgx.Row) (*UserObserver, error) {
	var o UserObserver
	err := row.Scan(&o.ID, &o.PublicID, &o.UserID, &o.PublicKeyHex, &o.CreatedAt,
		&o.Name, &o.FirmwareVersion, &o.ClientVersion, &o.Model, &o.IATA, &o.LastSeenAt, &o.BatteryMV, &o.NoiseFloor,
		&o.OrgCount)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// CreateUserObserver adds the observer with publicKeyHex (already validated and
// lowercased) to the user's list. Returns ErrDuplicate if it's already there,
// or ErrObserverLimit if the list is full.
func (s *Store) CreateUserObserver(ctx context.Context, userID int64, publicKeyHex string) (*UserObserver, error) {
	publicID, err := randomPublicID()
	if err != nil {
		return nil, err
	}
	var id int64
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		// Serialize this user's adds so two concurrent ones can't both pass the
		// count check.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID); err != nil {
			return fmt.Errorf("lock user: %w", err)
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM user_observers WHERE user_id = $1`, userID).Scan(&n); err != nil {
			return fmt.Errorf("count observers: %w", err)
		}
		if n >= MaxObserversPerUser {
			return ErrObserverLimit
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO user_observers (public_id, user_id, public_key_hex) VALUES ($1, $2, $3)
			RETURNING id`, publicID, userID, publicKeyHex).Scan(&id)
		if isUniqueViolation(err) {
			return ErrDuplicate
		}
		return err
	})
	if err != nil {
		if errors.Is(err, ErrDuplicate) || errors.Is(err, ErrObserverLimit) {
			return nil, err
		}
		return nil, fmt.Errorf("create observer: %w", err)
	}
	o, err := scanUserObserver(s.pool.QueryRow(ctx, userObserverSelect+` WHERE uo.id = $1`, id))
	return o, notFoundOr(err, "get created observer")
}

// ListObserversForUser returns the user's observers in the order they were
// added (at most MaxObserversPerUser).
func (s *Store) ListObserversForUser(ctx context.Context, userID int64) ([]*UserObserver, error) {
	rows, err := s.pool.Query(ctx, userObserverSelect+`
		WHERE uo.user_id = $1 ORDER BY uo.created_at, uo.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list observers: %w", err)
	}
	return collectRows(rows, scanUserObserver)
}

// GetUserObserver returns one of the user's observers by its public ID, or
// ErrNotFound (also for another user's).
func (s *Store) GetUserObserver(ctx context.Context, userID int64, publicID string) (*UserObserver, error) {
	o, err := scanUserObserver(s.pool.QueryRow(ctx, userObserverSelect+`
		WHERE uo.user_id = $1 AND uo.public_id = $2`, userID, publicID))
	return o, notFoundOr(err, "get observer")
}

// DeleteUserObserver removes one of the user's observers, or returns
// ErrNotFound.
func (s *Store) DeleteUserObserver(ctx context.Context, userID int64, publicID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM user_observers WHERE user_id = $1 AND public_id = $2`, userID, publicID)
	if err != nil {
		return fmt.Errorf("delete observer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ObserverReport is one organization's record of an observer.
type ObserverReport struct {
	OrgName    string
	OrgSlug    string
	Name       string // what the organization lists it as
	IATA       string
	ApprovedAt *time.Time
	LastSeenAt *time.Time
	// Member: the viewing user belongs to the organization. Only those are
	// named to them; the rest are just counted, since adding a key to your
	// list shouldn't reveal which organizations it reports to.
	Member bool
}

// Approved reports whether the organization's admins approved the observer.
func (r ObserverReport) Approved() bool { return r.ApprovedAt != nil }

// ObserverReports lists the organizations that have the observer with
// publicKeyHex, most recently heard first (at most maxObserverReports), marking
// those userID is a member of.
func (s *Store) ObserverReports(ctx context.Context, publicKeyHex string, userID int64) ([]ObserverReport, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT org.name, org.slug, o.name, o.iata, o.approved_at, o.last_seen_at, m.user_id IS NOT NULL
		FROM org_observers o
		JOIN organizations org ON org.id = o.org_id
		LEFT JOIN org_members m ON m.org_id = o.org_id AND m.user_id = $2
		WHERE o.public_key_hex = $1
		ORDER BY o.last_seen_at DESC NULLS LAST, lower(org.name)
		LIMIT $3`, publicKeyHex, userID, maxObserverReports)
	if err != nil {
		return nil, fmt.Errorf("list observer reports: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (ObserverReport, error) {
		var o ObserverReport
		err := r.Scan(&o.OrgName, &o.OrgSlug, &o.Name, &o.IATA, &o.ApprovedAt, &o.LastSeenAt, &o.Member)
		return o, err
	})
}
