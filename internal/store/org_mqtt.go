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
	_, err := s.pool.Exec(ctx, `
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
	if err != nil {
		return fmt.Errorf("upsert org broker: %w", err)
	}
	return nil
}

// DeleteOrgBroker removes the org's broker; the ingester disconnects on its next
// reconcile. Observers stay registered.
func (s *Store) DeleteOrgBroker(ctx context.Context, orgID int64) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM org_mqtt_brokers WHERE org_id = $1`, orgID); err != nil {
		return fmt.Errorf("delete org broker: %w", err)
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

// OrgObserver is an uplinking node registered to an org, with its last
// self-reported health.
type OrgObserver struct {
	ID              int64
	OrgID           int64
	Name            string
	PublicKeyHex    string
	CreatedAt       time.Time
	LastSeenAt      *time.Time
	ClientVersion   string
	FirmwareVersion string
	Model           string
	BatteryMV       *int
	NoiseFloor      *int
}

// CreateOrgObserver registers an observer for the org. Returns ErrDuplicate if
// the org already registered that key.
func (s *Store) CreateOrgObserver(ctx context.Context, orgID int64, name, publicKeyHex string, createdBy int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO org_observers (org_id, name, public_key_hex, created_by) VALUES ($1, $2, $3, $4)`,
		orgID, name, publicKeyHex, createdBy)
	if isUniqueViolation(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("create org observer: %w", err)
	}
	return nil
}

// DeleteOrgObserver removes one of the org's observers. Its past sightings stay:
// they're evidence about the companion, not the observer.
func (s *Store) DeleteOrgObserver(ctx context.Context, orgID, observerID int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM org_observers WHERE org_id = $1 AND id = $2`, orgID, observerID)
	if err != nil {
		return fmt.Errorf("delete org observer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListOrgObservers returns the org's observers by name. Unpaginated: an org
// registers a handful of uplinks, and only its admins can add them.
func (s *Store) ListOrgObservers(ctx context.Context, orgID int64) ([]OrgObserver, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, name, public_key_hex, created_at, last_seen_at,
		       client_version, firmware_version, model, battery_mv, noise_floor
		FROM org_observers WHERE org_id = $1
		ORDER BY lower(name), id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list org observers: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (OrgObserver, error) {
		var o OrgObserver
		err := r.Scan(&o.ID, &o.OrgID, &o.Name, &o.PublicKeyHex, &o.CreatedAt, &o.LastSeenAt,
			&o.ClientVersion, &o.FirmwareVersion, &o.Model, &o.BatteryMV, &o.NoiseFloor)
		return o, err
	})
}

// IsOrgObserver reports whether key is registered as an observer of the org.
func (s *Store) IsOrgObserver(ctx context.Context, orgID int64, publicKeyHex string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM org_observers WHERE org_id = $1 AND public_key_hex = $2)`,
		orgID, publicKeyHex).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("is org observer: %w", err)
	}
	return ok, nil
}

// TouchObserver marks the org's observer as heard at `at`, and when status is
// non-nil also stores its self-reported health. Fields the uplink didn't report
// keep their previous value.
func (s *Store) TouchObserver(ctx context.Context, orgID int64, publicKeyHex string, at time.Time, status *observe.Status) error {
	var err error
	if status == nil {
		_, err = s.pool.Exec(ctx, `
			UPDATE org_observers SET last_seen_at = GREATEST(last_seen_at, $3)
			WHERE org_id = $1 AND public_key_hex = $2`, orgID, publicKeyHex, at)
	} else {
		_, err = s.pool.Exec(ctx, `
			UPDATE org_observers SET
				last_seen_at = GREATEST(last_seen_at, $3),
				client_version = COALESCE(NULLIF($4, ''), client_version),
				firmware_version = COALESCE(NULLIF($5, ''), firmware_version),
				model = COALESCE(NULLIF($6, ''), model),
				battery_mv = COALESCE($7, battery_mv),
				noise_floor = COALESCE($8, noise_floor)
			WHERE org_id = $1 AND public_key_hex = $2`,
			orgID, publicKeyHex, at, status.ClientVersion, status.FirmwareVersion, status.Model,
			status.BatteryMV, status.NoiseFloor)
	}
	if err != nil {
		return fmt.Errorf("touch observer: %w", err)
	}
	return nil
}
