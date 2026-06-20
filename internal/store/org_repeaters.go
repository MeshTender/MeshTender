package store

import (
	"context"
	"fmt"
)

// RepeaterOrg describes an org a repeater is contributed to, with the version
// the owner consented to vs the org's current version (current > consented means
// re-consent is available).
type RepeaterOrg struct {
	OrgID            int64
	OrgSlug          string
	OrgName          string
	ConsentedVersion int
	CurrentVersion   int
}

// NeedsReconsent reports whether the org has published a newer version than the
// owner consented to.
func (r RepeaterOrg) NeedsReconsent() bool { return r.CurrentVersion > r.ConsentedVersion }

// ContributeRepeater contributes a repeater to an org pinned to consentedVersionID
// (also used to re-consent: re-pins to a newer version).
func (s *Store) ContributeRepeater(ctx context.Context, orgID, repeaterID, consentedVersionID, contributedBy int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO org_repeaters (org_id, repeater_id, consented_version_id, contributed_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (org_id, repeater_id)
		DO UPDATE SET consented_version_id = EXCLUDED.consented_version_id,
		              contributed_by = EXCLUDED.contributed_by, contributed_at = now()`,
		orgID, repeaterID, consentedVersionID, contributedBy)
	if err != nil {
		return fmt.Errorf("contribute repeater: %w", err)
	}
	return nil
}

// WithdrawRepeater removes a repeater from an org.
func (s *Store) WithdrawRepeater(ctx context.Context, orgID, repeaterID int64) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM org_repeaters WHERE org_id = $1 AND repeater_id = $2`, orgID, repeaterID)
	if err != nil {
		return fmt.Errorf("withdraw repeater: %w", err)
	}
	return nil
}

// OrgRepeaterInfo is a contributed repeater shown on the org page.
type OrgRepeaterInfo struct {
	RepeaterID       int64
	RepeaterPublicID string
	Name             string
	OwnerName        string
	HasLocation      bool
	Lat, Lon         float64
}

// ListOrgRepeaters returns the repeaters contributed to an org (with location
// when the owner consented to storing it).
func (s *Store) ListOrgRepeaters(ctx context.Context, orgID int64) ([]OrgRepeaterInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.public_id, r.name, COALESCE(NULLIF(ou.display_name, ''), ou.username, '?'),
		       r.latitude, r.longitude
		FROM org_repeaters orp
		JOIN repeaters r ON r.id = orp.repeater_id
		JOIN users ou ON ou.id = r.owner_id
		WHERE orp.org_id = $1
		ORDER BY r.name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list org repeaters: %w", err)
	}
	defer rows.Close()
	var out []OrgRepeaterInfo
	for rows.Next() {
		var ri OrgRepeaterInfo
		var lat, lon *float64
		if err := rows.Scan(&ri.RepeaterID, &ri.RepeaterPublicID, &ri.Name, &ri.OwnerName, &lat, &lon); err != nil {
			return nil, fmt.Errorf("scan org repeater: %w", err)
		}
		if lat != nil && lon != nil {
			ri.HasLocation, ri.Lat, ri.Lon = true, *lat, *lon
		}
		out = append(out, ri)
	}
	return out, rows.Err()
}

// ListPublicMapRepeaters returns the contributed repeaters an org may show on
// its public map: those whose owner opted into public_map and have coordinates.
func (s *Store) ListPublicMapRepeaters(ctx context.Context, orgID int64) ([]OrgRepeaterInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.name, COALESCE(NULLIF(ou.display_name, ''), ou.username, '?'),
		       r.latitude, r.longitude
		FROM org_repeaters orp
		JOIN repeaters r ON r.id = orp.repeater_id
		JOIN users ou ON ou.id = r.owner_id
		WHERE orp.org_id = $1 AND r.public_map AND r.latitude IS NOT NULL AND r.longitude IS NOT NULL
		ORDER BY r.name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list public map repeaters: %w", err)
	}
	defer rows.Close()
	var out []OrgRepeaterInfo
	for rows.Next() {
		var ri OrgRepeaterInfo
		var lat, lon *float64
		if err := rows.Scan(&ri.RepeaterID, &ri.Name, &ri.OwnerName, &lat, &lon); err != nil {
			return nil, fmt.Errorf("scan public map repeater: %w", err)
		}
		if lat != nil && lon != nil {
			ri.HasLocation, ri.Lat, ri.Lon = true, *lat, *lon
		}
		out = append(out, ri)
	}
	return out, rows.Err()
}

// ConsentedVersionID returns the permission version a repeater is pinned to for
// an org, or (0, false) if it isn't contributed there.
func (s *Store) ConsentedVersionID(ctx context.Context, orgID, repeaterID int64) (int64, bool, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`SELECT consented_version_id FROM org_repeaters WHERE org_id = $1 AND repeater_id = $2`,
		orgID, repeaterID).Scan(&id)
	if err != nil {
		return 0, false, nil //nolint:nilerr // absence is not an error here
	}
	return id, true, nil
}

// OwnedRepeatersNeedingReconsent returns the set of repeater ids owned by the
// user that are contributed to an org which has published a newer version than
// the owner consented to.
func (s *Store) OwnedRepeatersNeedingReconsent(ctx context.Context, ownerID int64) (map[int64]bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT orp.repeater_id
		FROM org_repeaters orp
		JOIN repeaters r ON r.id = orp.repeater_id AND r.owner_id = $1
		JOIN org_permission_versions cv ON cv.id = orp.consented_version_id
		WHERE cv.version < (SELECT max(version) FROM org_permission_versions WHERE org_id = orp.org_id)`,
		ownerID)
	if err != nil {
		return nil, fmt.Errorf("reconsent set: %w", err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ListRepeaterOrgs returns the orgs a repeater is contributed to, with consented
// vs current version numbers.
func (s *Store) ListRepeaterOrgs(ctx context.Context, repeaterID int64) ([]RepeaterOrg, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.slug, o.name, cv.version,
		       (SELECT max(version) FROM org_permission_versions WHERE org_id = o.id)
		FROM org_repeaters orp
		JOIN organizations o ON o.id = orp.org_id
		JOIN org_permission_versions cv ON cv.id = orp.consented_version_id
		WHERE orp.repeater_id = $1
		ORDER BY o.name`, repeaterID)
	if err != nil {
		return nil, fmt.Errorf("list repeater orgs: %w", err)
	}
	defer rows.Close()
	var out []RepeaterOrg
	for rows.Next() {
		var ro RepeaterOrg
		if err := rows.Scan(&ro.OrgID, &ro.OrgSlug, &ro.OrgName, &ro.ConsentedVersion, &ro.CurrentVersion); err != nil {
			return nil, fmt.Errorf("scan repeater org: %w", err)
		}
		out = append(out, ro)
	}
	return out, rows.Err()
}
