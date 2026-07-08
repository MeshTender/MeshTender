package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Repeater is a MeshCore repeater registered by a user.
type Repeater struct {
	ID int64
	// PublicID is the opaque, non-enumerable identifier used in URLs.
	PublicID     string
	OwnerID      int64
	Name         string
	PublicKeyHex string
	RadioFreqHz  int64
	RadioBwHz    int64
	RadioSF      int16
	RadioCR      int16
	Confirmed    bool
	ConfirmedAt  *time.Time
	CreatedAt    time.Time
	// Access level learned at confirm time; nil until determined.
	ConfirmedAdmin *bool
	ConfirmedPerms *int16
	// Location, fetched during the modem test (nil until determined).
	Latitude  *float64
	Longitude *float64
	// ShowOnPublicOrg shows this repeater on the public organization page — its
	// list, plus the map when coordinates are known.
	ShowOnPublicOrg bool
	// ExposePublicPage publishes a read-only public page for this repeater at its
	// public_id URL (the NFC/QR tap target). Distinct consent from ShowOnPublicOrg.
	ExposePublicPage bool
	// Documentation kept with the node. DocPublic is shown on the public page;
	// DocInternal (site-access details) only to people with access.
	DocPublic   string
	DocInternal string
	// Shared is true when the row is visible to the querying user via a share
	// rather than ownership.
	Shared bool
	// Owner identity, for display on shared repeaters.
	OwnerUsername    string
	OwnerDisplayName *string
	// Confirmation provenance, derived from repeater_confirmations:
	// SelfConfirmed = the owner reached it; Corroborators = distinct non-owner
	// names that also reached it.
	SelfConfirmed bool
	Corroborators []string
}

// Corroborated reports whether someone other than the owner has confirmed the
// repeater is reachable.
func (r *Repeater) Corroborated() bool { return len(r.Corroborators) > 0 }

// AccessKnown reports whether the repeater's access level has been determined.
func (r *Repeater) AccessKnown() bool { return r.ConfirmedAdmin != nil }

// IsAdmin reports whether the last confirmation granted admin access.
func (r *Repeater) IsAdmin() bool { return r.ConfirmedAdmin != nil && *r.ConfirmedAdmin }

// Perms returns the confirmed permission level, or 0 if unknown.
func (r *Repeater) Perms() int16 {
	if r.ConfirmedPerms != nil {
		return *r.ConfirmedPerms
	}
	return 0
}

// OwnerName returns the owner's display name if set, else their username.
func (r *Repeater) OwnerName() string {
	if r.OwnerDisplayName != nil && *r.OwnerDisplayName != "" {
		return *r.OwnerDisplayName
	}
	return r.OwnerUsername
}

// CreateRepeater inserts a repeater owned by ownerID. Returns ErrDuplicate if
// the owner already registered a repeater with the same public key.
func (s *Store) CreateRepeater(ctx context.Context, r *Repeater) (*Repeater, error) {
	publicID, err := randomPublicID()
	if err != nil {
		return nil, err
	}
	var out Repeater
	err = s.pool.QueryRow(ctx, `
		INSERT INTO repeaters (public_id, owner_id, name, public_key_hex, radio_freq_hz, radio_bw_hz, radio_sf, radio_cr, show_on_public_org)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, public_id, owner_id, name, public_key_hex, radio_freq_hz, radio_bw_hz, radio_sf, radio_cr, confirmed, confirmed_at, created_at, show_on_public_org`,
		publicID, r.OwnerID, r.Name, r.PublicKeyHex, r.RadioFreqHz, r.RadioBwHz, r.RadioSF, r.RadioCR, r.ShowOnPublicOrg).
		Scan(&out.ID, &out.PublicID, &out.OwnerID, &out.Name, &out.PublicKeyHex, &out.RadioFreqHz, &out.RadioBwHz,
			&out.RadioSF, &out.RadioCR, &out.Confirmed, &out.ConfirmedAt, &out.CreatedAt, &out.ShowOnPublicOrg)
	if isUniqueViolation(err) {
		return nil, ErrDuplicate
	}
	if err != nil {
		return nil, fmt.Errorf("create repeater: %w", err)
	}
	return &out, nil
}

// repeaterSelect joins the owner for display. $1 is always the querying user id
// (used to compute the Shared flag).
const repeaterSelect = `
	SELECT r.id, r.public_id, r.owner_id, r.name, r.public_key_hex, r.radio_freq_hz, r.radio_bw_hz,
	       r.radio_sf, r.radio_cr, r.confirmed, r.confirmed_at, r.created_at,
	       r.confirmed_admin, r.confirmed_perms,
	       r.latitude, r.longitude, r.show_on_public_org, r.expose_public_page, r.doc_public, r.doc_internal,
	       (r.owner_id <> $1) AS shared, ou.username, ou.display_name,
	       EXISTS(SELECT 1 FROM repeater_confirmations c
	              WHERE c.repeater_id = r.id AND c.user_id = r.owner_id) AS self_confirmed,
	       ARRAY(SELECT DISTINCT COALESCE(NULLIF(cu.display_name, ''), cu.username)
	             FROM repeater_confirmations c JOIN users cu ON cu.id = c.user_id
	             WHERE c.repeater_id = r.id AND c.user_id <> r.owner_id) AS corroborators
	FROM repeaters r JOIN users ou ON ou.id = r.owner_id`

func scanRepeater(row pgx.Row) (*Repeater, error) {
	var r Repeater
	err := row.Scan(&r.ID, &r.PublicID, &r.OwnerID, &r.Name, &r.PublicKeyHex, &r.RadioFreqHz, &r.RadioBwHz,
		&r.RadioSF, &r.RadioCR, &r.Confirmed, &r.ConfirmedAt, &r.CreatedAt,
		&r.ConfirmedAdmin, &r.ConfirmedPerms,
		&r.Latitude, &r.Longitude, &r.ShowOnPublicOrg, &r.ExposePublicPage, &r.DocPublic, &r.DocInternal,
		&r.Shared, &r.OwnerUsername, &r.OwnerDisplayName,
		&r.SelfConfirmed, &r.Corroborators)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRepeatersForUser returns repeaters the user owns or has been granted
// access to, owned ones first and then by name within each group.
func (s *Store) ListRepeatersForUser(ctx context.Context, userID int64) ([]*Repeater, error) {
	// Dashboard listing: repeaters the user owns or has a direct share on. Org-
	// contributed repeaters are reached from the org page, not listed here.
	rows, err := s.pool.Query(ctx, repeaterSelect+`
		WHERE r.owner_id = $1
		   OR r.id IN (SELECT repeater_id FROM repeater_shares WHERE user_id = $1)
		ORDER BY shared, lower(r.name), r.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list repeaters: %w", err)
	}
	out, err := collectRows(rows, scanRepeater)
	if err != nil {
		return nil, fmt.Errorf("scan repeater: %w", err)
	}
	return out, nil
}

// OwnsAnyRepeater reports whether the user owns at least one repeater.
func (s *Store) OwnsAnyRepeater(ctx context.Context, ownerID int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM repeaters WHERE owner_id = $1)`, ownerID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("owns any repeater: %w", err)
	}
	return ok, nil
}

// GetRepeaterForUser returns a single repeater if the user owns it or has a
// share, else ErrNotFound. This is the authorization gate for control actions.
func (s *Store) GetRepeaterForUser(ctx context.Context, userID, repeaterID int64) (*Repeater, error) {
	row := s.pool.QueryRow(ctx, repeaterSelect+`
		WHERE r.id = $2
		  AND (r.owner_id = $1
		       OR r.id IN (SELECT repeater_id FROM repeater_shares WHERE user_id = $1)
		       OR EXISTS (SELECT 1
		                  FROM org_members ownm
		                  JOIN org_members usrm ON usrm.org_id = ownm.org_id AND usrm.user_id = $1
		                  WHERE ownm.user_id = r.owner_id
		                    AND NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
		                                    WHERE e.org_id = ownm.org_id AND e.repeater_id = r.id)))`,
		userID, repeaterID)
	r, err := scanRepeater(row)
	if err != nil {
		return nil, notFoundOr(err, "get repeater")
	}
	return r, nil
}

// RepeaterIDByPublicID resolves a URL public_id to the internal int64 primary
// key, or ErrNotFound. Authorization is enforced separately by the per-user
// GetRepeaterForUser / GetRepeaterOwned gates.
func (s *Store) RepeaterIDByPublicID(ctx context.Context, publicID string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM repeaters WHERE public_id = $1`, publicID).Scan(&id)
	if err != nil {
		return 0, notFoundOr(err, "repeater by public id")
	}
	return id, nil
}

// SetRepeaterConfirmed marks a repeater confirmed by userID, recording the
// access level learned from the login reply. It updates the cached "latest"
// columns on the repeater and appends a row to the confirmation history (so a
// non-owner confirmation can corroborate the owner's own). Both writes happen
// in one transaction.
func (s *Store) SetRepeaterConfirmed(ctx context.Context, repeaterID, userID int64, admin bool, perms int16) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE repeaters
			SET confirmed = TRUE, confirmed_at = now(), confirmed_admin = $2, confirmed_perms = $3
			WHERE id = $1`, repeaterID, admin, perms); err != nil {
			return fmt.Errorf("set confirmed: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO repeater_confirmations (repeater_id, user_id, is_admin, perms)
			VALUES ($1, $2, $3, $4)`, repeaterID, userID, admin, perms); err != nil {
			return fmt.Errorf("record confirmation: %w", err)
		}
		return nil
	})
}

// UpdateRepeater updates an owned repeater's settings (the public key is fixed).
// Returns ErrNotFound if the repeater isn't owned by ownerID.
func (s *Store) UpdateRepeater(ctx context.Context, ownerID, repeaterID int64, name string, freq, bw int64, sf, cr int16, showOnPublicOrg, exposePublicPage bool) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE repeaters SET
			name = $3, radio_freq_hz = $4, radio_bw_hz = $5, radio_sf = $6, radio_cr = $7,
			show_on_public_org = $8, expose_public_page = $9
		WHERE id = $1 AND owner_id = $2`,
		repeaterID, ownerID, name, freq, bw, sf, cr, showOnPublicOrg, exposePublicPage)
	if err != nil {
		return fmt.Errorf("update repeater: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateRepeaterDocs saves an owned repeater's documentation (public and
// internal). Returns ErrNotFound if the repeater isn't owned by ownerID.
func (s *Store) UpdateRepeaterDocs(ctx context.Context, ownerID, repeaterID int64, docPublic, docInternal string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE repeaters SET doc_public = $3, doc_internal = $4
		WHERE id = $1 AND owner_id = $2`,
		repeaterID, ownerID, docPublic, docInternal)
	if err != nil {
		return fmt.Errorf("update repeater docs: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetRepeaterPublic returns a repeater by its public_id only if its owner has
// published a public page (expose_public_page), else ErrNotFound. No user scope:
// this backs the anonymous public page (the NFC/QR tap target).
func (s *Store) GetRepeaterPublic(ctx context.Context, publicID string) (*Repeater, error) {
	// $1 = 0 (no querying user): the Shared flag is irrelevant for the public view.
	row := s.pool.QueryRow(ctx, repeaterSelect+`
		WHERE r.public_id = $2 AND r.expose_public_page`, int64(0), publicID)
	r, err := scanRepeater(row)
	if err != nil {
		return nil, notFoundOr(err, "get public repeater")
	}
	return r, nil
}

// SetRepeaterLocation stores a repeater's location fetched during the modem test.
func (s *Store) SetRepeaterLocation(ctx context.Context, repeaterID int64, lat, lon float64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE repeaters SET latitude = $2, longitude = $3 WHERE id = $1`,
		repeaterID, lat, lon)
	if err != nil {
		return fmt.Errorf("set location: %w", err)
	}
	return nil
}

// SetRepeaterLatitude stores only a repeater's latitude, leaving the longitude
// untouched. Used when the console reads a single coordinate ("get lat"), which
// arrives independently of the other and must not clobber it.
func (s *Store) SetRepeaterLatitude(ctx context.Context, repeaterID int64, lat float64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE repeaters SET latitude = $2 WHERE id = $1`, repeaterID, lat)
	if err != nil {
		return fmt.Errorf("set latitude: %w", err)
	}
	return nil
}

// SetRepeaterLongitude stores only a repeater's longitude, leaving the latitude
// untouched. See SetRepeaterLatitude.
func (s *Store) SetRepeaterLongitude(ctx context.Context, repeaterID int64, lon float64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE repeaters SET longitude = $2 WHERE id = $1`, repeaterID, lon)
	if err != nil {
		return fmt.Errorf("set longitude: %w", err)
	}
	return nil
}

// DeleteRepeaterOwned deletes a repeater only if owned by ownerID.
func (s *Store) DeleteRepeaterOwned(ctx context.Context, ownerID, repeaterID int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM repeaters WHERE id = $1 AND owner_id = $2`, repeaterID, ownerID)
	if err != nil {
		return fmt.Errorf("delete repeater: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
