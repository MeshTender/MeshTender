package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// A repeater participates in an org iff its owner is a member of that org and the
// owner hasn't opted it out (no org_repeater_excludes row). This file builds the
// org↔repeater listings around that rule and manages the opt-out set.

// RepeaterOrg is an org a repeater participates in.
type RepeaterOrg struct {
	OrgID   int64
	OrgSlug string
	OrgName string
}

// RepeaterOrgMembership is an org the repeater's owner belongs to, with whether
// the owner has opted this repeater out of it — drives the per-org include/exclude
// toggles on the owner's repeater/share pages.
type RepeaterOrgMembership struct {
	OrgID    int64
	OrgSlug  string
	OrgName  string
	Excluded bool
}

// SetRepeaterOrgExcluded opts a repeater out of (excluded=true) or back into
// (excluded=false) an org. Idempotent.
func (s *Store) SetRepeaterOrgExcluded(ctx context.Context, orgID, repeaterID int64, excluded bool) error {
	var err error
	if excluded {
		_, err = s.pool.Exec(ctx,
			`INSERT INTO org_repeater_excludes (org_id, repeater_id) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`, orgID, repeaterID)
	} else {
		_, err = s.pool.Exec(ctx,
			`DELETE FROM org_repeater_excludes WHERE org_id = $1 AND repeater_id = $2`, orgID, repeaterID)
	}
	if err != nil {
		return fmt.Errorf("set repeater org excluded: %w", err)
	}
	return nil
}

// OrgRepeaterInfo is a participating repeater shown on the org page.
type OrgRepeaterInfo struct {
	RepeaterID       int64
	RepeaterPublicID string
	Name             string
	OwnerName        string
	HasLocation      bool
	Lat, Lon         float64
}

// ListOrgRepeaters returns the repeaters participating in an org (owned by a
// member, not opted out), with location when the owner stored it.
func (s *Store) ListOrgRepeaters(ctx context.Context, orgID int64) ([]OrgRepeaterInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.public_id, r.name, COALESCE(NULLIF(ou.display_name, ''), ou.username, '?'),
		       r.latitude, r.longitude
		FROM repeaters r
		JOIN org_members om ON om.org_id = $1 AND om.user_id = r.owner_id
		JOIN users ou ON ou.id = r.owner_id
		WHERE NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
		                  WHERE e.org_id = $1 AND e.repeater_id = r.id)
		ORDER BY r.name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list org repeaters: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (OrgRepeaterInfo, error) {
		var ri OrgRepeaterInfo
		var lat, lon *float64
		err := r.Scan(&ri.RepeaterID, &ri.RepeaterPublicID, &ri.Name, &ri.OwnerName, &lat, &lon)
		setLocation(&ri, lat, lon)
		return ri, err
	})
}

// setLocation fills the optional location fields on ri when both coordinates are
// present (the owner consented to storing them).
func setLocation(ri *OrgRepeaterInfo, lat, lon *float64) {
	if lat != nil && lon != nil {
		ri.HasLocation, ri.Lat, ri.Lon = true, *lat, *lon
	}
}

// ListPublicRepeaters returns the participating repeaters an org may show on its
// public page: those whose owner opted into public_map. Coordinates are included
// when known (HasLocation), so the same set drives both the public list (all of
// them) and the public map (only those with coordinates).
func (s *Store) ListPublicRepeaters(ctx context.Context, orgID int64) ([]OrgRepeaterInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.name, COALESCE(NULLIF(ou.display_name, ''), ou.username, '?'),
		       r.latitude, r.longitude
		FROM repeaters r
		JOIN org_members om ON om.org_id = $1 AND om.user_id = r.owner_id
		JOIN users ou ON ou.id = r.owner_id
		WHERE r.public_map
		  AND NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
		                  WHERE e.org_id = $1 AND e.repeater_id = r.id)
		ORDER BY r.name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list public repeaters: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (OrgRepeaterInfo, error) {
		var ri OrgRepeaterInfo
		var lat, lon *float64
		err := r.Scan(&ri.RepeaterID, &ri.Name, &ri.OwnerName, &lat, &lon)
		setLocation(&ri, lat, lon)
		return ri, err
	})
}

// ListRepeaterOrgs returns the orgs a repeater participates in (owner is a member
// and hasn't opted it out).
func (s *Store) ListRepeaterOrgs(ctx context.Context, repeaterID int64) ([]RepeaterOrg, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.slug, o.name
		FROM repeaters r
		JOIN org_members om ON om.user_id = r.owner_id
		JOIN organizations o ON o.id = om.org_id
		WHERE r.id = $1
		  AND NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
		                  WHERE e.org_id = o.id AND e.repeater_id = r.id)
		ORDER BY o.name`, repeaterID)
	if err != nil {
		return nil, fmt.Errorf("list repeater orgs: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (RepeaterOrg, error) {
		var ro RepeaterOrg
		err := r.Scan(&ro.OrgID, &ro.OrgSlug, &ro.OrgName)
		return ro, err
	})
}

// ListRepeaterOrgMemberships returns every org the repeater's owner belongs to,
// flagged with whether the owner has opted this repeater out of it.
func (s *Store) ListRepeaterOrgMemberships(ctx context.Context, repeaterID int64) ([]RepeaterOrgMembership, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.slug, o.name,
		       EXISTS (SELECT 1 FROM org_repeater_excludes e
		               WHERE e.org_id = o.id AND e.repeater_id = r.id)
		FROM repeaters r
		JOIN org_members om ON om.user_id = r.owner_id
		JOIN organizations o ON o.id = om.org_id
		WHERE r.id = $1
		ORDER BY o.name`, repeaterID)
	if err != nil {
		return nil, fmt.Errorf("list repeater org memberships: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (RepeaterOrgMembership, error) {
		var m RepeaterOrgMembership
		err := r.Scan(&m.OrgID, &m.OrgSlug, &m.OrgName, &m.Excluded)
		return m, err
	})
}
