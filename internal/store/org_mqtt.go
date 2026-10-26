package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/MeshTender/MeshTender/internal/observe"
)

// An org's MQTT broker and the observers it vouches for. The ingester connects
// to each enabled broker and records adverts only from that org's registered
// observers; see internal/ingest.

// BrokersChangedChannel is notified (Postgres NOTIFY) whenever an org's broker
// settings are saved or removed, so the ingester reconnects at once instead of
// on its next periodic reconcile. Delivered only when the change commits.
const BrokersChangedChannel = "meshtender_brokers_changed"

// OrgBroker is an org's MQTT broker configuration plus the ingester's last
// reported status. PasswordSealed is secretbox-sealed; it never leaves the
// server, and the UI only learns whether one is set.
type OrgBroker struct {
	OrgID           int64
	URL             string
	Username        string
	PasswordSealed  []byte
	TopicPrefix     string
	Enabled         bool
	UpdatedAt       time.Time
	LastConnectedAt *time.Time
	LastMessageAt   *time.Time
	LastError       string
}

// HasPassword reports whether a password is stored.
func (b *OrgBroker) HasPassword() bool { return len(b.PasswordSealed) > 0 }

const orgBrokerSelect = `
	SELECT org_id, url, username, password_sealed, topic_prefix, enabled, updated_at,
	       last_connected_at, last_message_at, last_error
	FROM org_mqtt_brokers`

func scanOrgBroker(row pgx.Row) (*OrgBroker, error) {
	var b OrgBroker
	err := row.Scan(&b.OrgID, &b.URL, &b.Username, &b.PasswordSealed, &b.TopicPrefix, &b.Enabled,
		&b.UpdatedAt, &b.LastConnectedAt, &b.LastMessageAt, &b.LastError)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// GetOrgBroker returns the org's broker, or ErrNotFound if none is configured.
func (s *Store) GetOrgBroker(ctx context.Context, orgID int64) (*OrgBroker, error) {
	b, err := scanOrgBroker(s.pool.QueryRow(ctx, orgBrokerSelect+` WHERE org_id = $1`, orgID))
	return b, notFoundOr(err, "get org broker")
}

// UpsertOrgBroker saves the org's broker settings. A nil passwordSealed keeps
// the stored password (the form's password field is write-only and blank means
// "unchanged"); clearPassword removes it. Saving resets the status columns: they
// described the previous settings.
func (s *Store) UpsertOrgBroker(ctx context.Context, b OrgBroker, passwordSealed []byte, clearPassword bool, updatedBy int64) error {
	return s.changeBrokers(ctx, "upsert org broker", `
		INSERT INTO org_mqtt_brokers (org_id, url, username, password_sealed, topic_prefix, enabled, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (org_id) DO UPDATE SET
			url = EXCLUDED.url,
			username = EXCLUDED.username,
			password_sealed = CASE WHEN $8 THEN NULL
			                       WHEN EXCLUDED.password_sealed IS NULL THEN org_mqtt_brokers.password_sealed
			                       ELSE EXCLUDED.password_sealed END,
			topic_prefix = EXCLUDED.topic_prefix,
			enabled = EXCLUDED.enabled,
			updated_by = EXCLUDED.updated_by,
			updated_at = now(),
			last_connected_at = NULL,
			last_message_at = NULL,
			last_error = ''`,
		b.OrgID, b.URL, b.Username, passwordSealed, b.TopicPrefix, b.Enabled, updatedBy, clearPassword)
}

// DeleteOrgBroker removes the org's broker; the ingester disconnects on its next
// reconcile. Observers stay registered.
func (s *Store) DeleteOrgBroker(ctx context.Context, orgID int64) error {
	return s.changeBrokers(ctx, "delete org broker", `DELETE FROM org_mqtt_brokers WHERE org_id = $1`, orgID)
}

// changeBrokers runs a write to org_mqtt_brokers and notifies
// BrokersChangedChannel in the same transaction, so the ingester hears about
// the change exactly when it becomes visible. Status writes from the ingester
// itself (RecordBrokerStatus) deliberately don't notify.
func (s *Store) changeBrokers(ctx context.Context, what, sql string, args ...any) error {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT pg_notify($1, '')`, BrokersChangedChannel)
		return err
	})
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// ListEnabledBrokers returns every enabled broker, for the ingester's reconcile.
// Bounded by the number of orgs (one broker each).
func (s *Store) ListEnabledBrokers(ctx context.Context) ([]*OrgBroker, error) {
	rows, err := s.pool.Query(ctx, orgBrokerSelect+` WHERE enabled ORDER BY org_id`)
	if err != nil {
		return nil, fmt.Errorf("list enabled brokers: %w", err)
	}
	return collectRows(rows, scanOrgBroker)
}

// RecordBrokerStatus stores the ingester's view of a broker connection. Nil
// times leave the column as is; lastError replaces the stored category (empty
// clears it).
func (s *Store) RecordBrokerStatus(ctx context.Context, orgID int64, connectedAt, messageAt *time.Time, lastError string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE org_mqtt_brokers SET
			last_connected_at = COALESCE($2, last_connected_at),
			last_message_at = COALESCE($3, last_message_at),
			last_error = $4
		WHERE org_id = $1`, orgID, connectedAt, messageAt, lastError)
	if err != nil {
		return fmt.Errorf("record broker status: %w", err)
	}
	return nil
}

// OrgObserver is an uplinking node known to an org, with its last
// self-reported health. Observers are added by hand (approved) or discovered
// from their status reports (unapproved until an admin approves them); only
// approved ones count for check-ins and net logs.
type OrgObserver struct {
	ID           int64
	OrgID        int64
	Name         string
	PublicKeyHex string
	// IATA is the region code from the observer's most recent status topic.
	IATA            string
	CreatedAt       time.Time
	ApprovedAt      *time.Time
	LastSeenAt      *time.Time
	ClientVersion   string
	FirmwareVersion string
	Model           string
	BatteryMV       *int
	NoiseFloor      *int
}

// Approved reports whether an admin approved the observer.
func (o OrgObserver) Approved() bool { return o.ApprovedAt != nil }

// ObserverOnlineWindow is how recently an observer must have been heard from to
// count as online. Busy observers uplink constantly; status reports come every
// few minutes even on a quiet mesh.
const ObserverOnlineWindow = 10 * time.Minute

// OnlineAt reports whether the observer was heard within ObserverOnlineWindow
// of now.
func (o OrgObserver) OnlineAt(now time.Time) bool {
	return o.LastSeenAt != nil && now.Sub(*o.LastSeenAt) < ObserverOnlineWindow
}

// MaxUnapprovedObservers caps how many unapproved observers discovery keeps per
// org. An org's broker may carry many uplinks that aren't its own; the cap
// bounds the (unpaginated) observer list should it point at a busy public one.
const MaxUnapprovedObservers = 500

// ObserverFilter selects observers by approval.
type ObserverFilter string

const (
	ObserversApproved   ObserverFilter = "approved"
	ObserversUnapproved ObserverFilter = "unapproved"
	ObserversAll        ObserverFilter = "all"
)

// ObserverCounts is how many of an org's observers are approved and not.
type ObserverCounts struct {
	Approved, Unapproved int
}

// CreateOrgObserver registers an observer for the org by hand, approved. An
// observer already discovered under that key is approved and given the name
// instead. Returns ErrDuplicate if the org already approved that key.
func (s *Store) CreateOrgObserver(ctx context.Context, orgID int64, name, publicKeyHex string, createdBy int64) error {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO org_observers (org_id, name, public_key_hex, created_by, approved_at, approved_by)
		VALUES ($1, $2, $3, $4, now(), $4)
		ON CONFLICT (org_id, public_key_hex) DO UPDATE SET
			name = EXCLUDED.name, approved_at = now(), approved_by = EXCLUDED.approved_by
		WHERE org_observers.approved_at IS NULL`,
		orgID, name, publicKeyHex, createdBy)
	if err != nil {
		return fmt.Errorf("create org observer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDuplicate
	}
	return nil
}

// ApproveOrgObserver approves one of the org's observers under the given name
// (renaming it if it's already approved).
func (s *Store) ApproveOrgObserver(ctx context.Context, orgID, observerID int64, name string, approvedBy int64) error {
	return s.updateOrgObserver(ctx, "approve org observer", `
		UPDATE org_observers SET name = $3,
			approved_at = COALESCE(approved_at, now()),
			approved_by = CASE WHEN approved_at IS NULL THEN $4 ELSE approved_by END
		WHERE org_id = $1 AND id = $2`, orgID, observerID, name, approvedBy)
}

// UnapproveOrgObserver withdraws approval: the observer stays listed (it keeps
// reporting) but no longer counts.
func (s *Store) UnapproveOrgObserver(ctx context.Context, orgID, observerID int64) error {
	return s.updateOrgObserver(ctx, "unapprove org observer", `
		UPDATE org_observers SET approved_at = NULL, approved_by = NULL
		WHERE org_id = $1 AND id = $2`, orgID, observerID)
}

// DeleteOrgObserver removes one of the org's observers. Its past sightings stay:
// they're evidence about the companion, not the observer. One that's still
// reporting is discovered again, unapproved.
func (s *Store) DeleteOrgObserver(ctx context.Context, orgID, observerID int64) error {
	return s.updateOrgObserver(ctx, "delete org observer",
		`DELETE FROM org_observers WHERE org_id = $1 AND id = $2`, orgID, observerID)
}

// updateOrgObserver runs a write to one of the org's observers, returning
// ErrNotFound if it matched none.
func (s *Store) updateOrgObserver(ctx context.Context, what, sql string, args ...any) error {
	tag, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const orgObserverSelect = `
	SELECT id, org_id, name, public_key_hex, iata, created_at, approved_at, last_seen_at,
	       client_version, firmware_version, model, battery_mv, noise_floor
	FROM org_observers`

func scanOrgObserver(r pgx.Row) (OrgObserver, error) {
	var o OrgObserver
	err := r.Scan(&o.ID, &o.OrgID, &o.Name, &o.PublicKeyHex, &o.IATA, &o.CreatedAt, &o.ApprovedAt, &o.LastSeenAt,
		&o.ClientVersion, &o.FirmwareVersion, &o.Model, &o.BatteryMV, &o.NoiseFloor)
	return o, err
}

// GetOrgObserver returns one of the org's observers, or ErrNotFound.
func (s *Store) GetOrgObserver(ctx context.Context, orgID, observerID int64) (*OrgObserver, error) {
	o, err := scanOrgObserver(s.pool.QueryRow(ctx, orgObserverSelect+` WHERE org_id = $1 AND id = $2`, orgID, observerID))
	if err != nil {
		return nil, notFoundOr(err, "get org observer")
	}
	return &o, nil
}

// ListOrgObservers returns the org's observers matching filter, by name.
// Unpaginated: an org approves a handful of uplinks, and discovery keeps at
// most MaxUnapprovedObservers more.
func (s *Store) ListOrgObservers(ctx context.Context, orgID int64, filter ObserverFilter) ([]OrgObserver, error) {
	where := ` WHERE org_id = $1`
	switch filter {
	case ObserversApproved:
		where += ` AND approved_at IS NOT NULL`
	case ObserversUnapproved:
		where += ` AND approved_at IS NULL`
	}
	rows, err := s.pool.Query(ctx, orgObserverSelect+where+` ORDER BY lower(name), id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list org observers: %w", err)
	}
	return collectRows(rows, scanOrgObserver)
}

// CountOrgObservers counts the org's approved and unapproved observers.
func (s *Store) CountOrgObservers(ctx context.Context, orgID int64) (ObserverCounts, error) {
	var c ObserverCounts
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE approved_at IS NOT NULL), count(*) FILTER (WHERE approved_at IS NULL)
		FROM org_observers WHERE org_id = $1`, orgID).Scan(&c.Approved, &c.Unapproved)
	if err != nil {
		return c, fmt.Errorf("count org observers: %w", err)
	}
	return c, nil
}

// IsOrgObserver reports whether key is an approved observer of the org.
func (s *Store) IsOrgObserver(ctx context.Context, orgID int64, publicKeyHex string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM org_observers
		              WHERE org_id = $1 AND public_key_hex = $2 AND approved_at IS NOT NULL)`,
		orgID, publicKeyHex).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("is org observer: %w", err)
	}
	return ok, nil
}

// TouchObserver marks the org's observer as heard at `at`, from packet
// traffic. It only updates an observer the org already has.
func (s *Store) TouchObserver(ctx context.Context, orgID int64, publicKeyHex string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE org_observers SET last_seen_at = GREATEST(last_seen_at, $3)
		WHERE org_id = $1 AND public_key_hex = $2`, orgID, publicKeyHex, at)
	if err != nil {
		return fmt.Errorf("touch observer: %w", err)
	}
	return nil
}

// ReportObserver records an observer's status report, heard at `at` under the
// region code iata. It updates the observer if the org has it — health fields
// the uplink didn't report keep their previous value, and the reported name
// replaces the stored one only while the observer is unapproved — and
// otherwise discovers it, unapproved, unless the org already has
// MaxUnapprovedObservers of those. The region code is the observer's to choose,
// so it's cut to 16 characters (they're IATA codes, three letters).
func (s *Store) ReportObserver(ctx context.Context, orgID int64, publicKeyHex, iata string, at time.Time, status *observe.Status) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE org_observers SET
			last_seen_at = GREATEST(last_seen_at, $3),
			iata = left($4, 16),
			name = CASE WHEN approved_at IS NULL AND $5 <> '' THEN $5 ELSE name END,
			client_version = COALESCE(NULLIF($6, ''), client_version),
			firmware_version = COALESCE(NULLIF($7, ''), firmware_version),
			model = COALESCE(NULLIF($8, ''), model),
			battery_mv = COALESCE($9, battery_mv),
			noise_floor = COALESCE($10, noise_floor)
		WHERE org_id = $1 AND public_key_hex = $2`,
		orgID, publicKeyHex, at, iata, status.Name, status.ClientVersion, status.FirmwareVersion, status.Model,
		status.BatteryMV, status.NoiseFloor)
	if err != nil {
		return fmt.Errorf("report observer: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	// The cap check counts at most MaxUnapprovedObservers rows on the
	// (org_id, public_key_hex) index. ON CONFLICT covers a row added since the
	// UPDATE; the next report updates it.
	_, err = s.pool.Exec(ctx, `
		INSERT INTO org_observers (org_id, name, public_key_hex, iata, last_seen_at,
		                           client_version, firmware_version, model, battery_mv, noise_floor)
		SELECT $1, $5, $2, left($4, 16), $3, $6, $7, $8, $9, $10
		WHERE (SELECT count(*) FROM org_observers WHERE org_id = $1 AND approved_at IS NULL) < $11
		ON CONFLICT (org_id, public_key_hex) DO NOTHING`,
		orgID, publicKeyHex, at, iata, status.Name, status.ClientVersion, status.FirmwareVersion, status.Model,
		status.BatteryMV, status.NoiseFloor, MaxUnapprovedObservers)
	if err != nil {
		return fmt.Errorf("discover observer: %w", err)
	}
	return nil
}
