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
// the owner has opted this repeater out of it and whether a per-repeater command
// restriction is in effect for it — drives the per-org toggles and the
// "limited commands" indicator on the owner's share page.
type RepeaterOrgMembership struct {
	OrgID    int64
	OrgSlug  string
	OrgName  string
	Excluded bool
	// Restricted is true when this repeater has a per-org command opt-in list for
	// this org (>=1 row); false means permissive (the org's full ceiling applies).
	Restricted bool
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

// IsRepeaterOrgExcluded reports whether the repeater is opted out of the org
// (an org_repeater_excludes row exists). Absence = participating.
func (s *Store) IsRepeaterOrgExcluded(ctx context.Context, orgID, repeaterID int64) (bool, error) {
	var excluded bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM org_repeater_excludes WHERE org_id = $1 AND repeater_id = $2)`,
		orgID, repeaterID).Scan(&excluded)
	if err != nil {
		return false, fmt.Errorf("is repeater org excluded: %w", err)
	}
	return excluded, nil
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

// ExcludeOwnerRepeatersFromOrg opts every repeater the owner currently has out of
// the org — used when a user joins "with no repeaters". Repeaters added later are
// not affected (they start shared, and can be opted out individually).
func (s *Store) ExcludeOwnerRepeatersFromOrg(ctx context.Context, orgID, ownerID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO org_repeater_excludes (org_id, repeater_id)
		SELECT $1, id FROM repeaters WHERE owner_id = $2
		ON CONFLICT DO NOTHING`, orgID, ownerID)
	if err != nil {
		return fmt.Errorf("exclude owner repeaters: %w", err)
	}
	return nil
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
// public page: those whose owner opted into show_on_public_org. Coordinates are
// included when known (HasLocation), so the same set drives both the public list
// (all of them) and the public map (only those with coordinates).
func (s *Store) ListPublicRepeaters(ctx context.Context, orgID int64) ([]OrgRepeaterInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.name, COALESCE(NULLIF(ou.display_name, ''), ou.username, '?'),
		       r.latitude, r.longitude
		FROM repeaters r
		JOIN org_members om ON om.org_id = $1 AND om.user_id = r.owner_id
		JOIN users ou ON ou.id = r.owner_id
		WHERE r.show_on_public_org
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

// OrgPublicRepeatersPageSize is the number of public repeaters listed per page on
// an org's public Repeaters tab.
const OrgPublicRepeatersPageSize = 25

// MapPoint is a located repeater plotted on an org's public map. The map shows
// every located public repeater regardless of which list page is in view. The
// JSON tags are the shape the public map endpoint serves to meshmap.js.
type MapPoint struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// ListPublicRepeatersPage returns one keyset page of an org's public repeaters
// ordered by name. afterName/afterID are the last row of the previous page (empty
// afterName starts at the beginning). The bool reports whether more pages follow.
func (s *Store) ListPublicRepeatersPage(ctx context.Context, orgID int64, afterName string, afterID int64) ([]OrgRepeaterInfo, bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.name, COALESCE(NULLIF(ou.display_name, ''), ou.username, '?')
		FROM repeaters r
		JOIN org_members om ON om.org_id = $1 AND om.user_id = r.owner_id
		JOIN users ou ON ou.id = r.owner_id
		WHERE r.show_on_public_org
		  AND NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
		                  WHERE e.org_id = $1 AND e.repeater_id = r.id)
		  AND ($2 = '' OR (lower(r.name), r.id) > (lower($2), $3))
		ORDER BY lower(r.name), r.id
		LIMIT $4`, orgID, afterName, afterID, OrgPublicRepeatersPageSize+1)
	if err != nil {
		return nil, false, fmt.Errorf("list public repeaters page: %w", err)
	}
	out, err := collectRows(rows, func(r pgx.Row) (OrgRepeaterInfo, error) {
		var ri OrgRepeaterInfo
		return ri, r.Scan(&ri.RepeaterID, &ri.Name, &ri.OwnerName)
	})
	if err != nil {
		return nil, false, err
	}
	hasMore := len(out) > OrgPublicRepeatersPageSize
	if hasMore {
		out = out[:OrgPublicRepeatersPageSize]
	}
	return out, hasMore, nil
}

// ListPublicRepeaterPoints returns every located public repeater for an org, for
// the map — independent of list paging so all coverage shows at once.
func (s *Store) ListPublicRepeaterPoints(ctx context.Context, orgID int64) ([]MapPoint, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.name, r.latitude, r.longitude
		FROM repeaters r
		JOIN org_members om ON om.org_id = $1 AND om.user_id = r.owner_id
		WHERE r.show_on_public_org
		  AND r.latitude IS NOT NULL AND r.longitude IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
		                  WHERE e.org_id = $1 AND e.repeater_id = r.id)
		ORDER BY lower(r.name), r.id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list public repeater points: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (MapPoint, error) {
		var p MapPoint
		return p, r.Scan(&p.Name, &p.Lat, &p.Lon)
	})
}

// HasPublicRepeaterPoints reports whether an org has at least one located public
// repeater — i.e. whether its public map is worth rendering. It's the cheap
// existence check the public pages use to decide whether to show the map
// container (and fetch the points) without pulling the full set into the HTML.
func (s *Store) HasPublicRepeaterPoints(ctx context.Context, orgID int64) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM repeaters r
			JOIN org_members om ON om.org_id = $1 AND om.user_id = r.owner_id
			WHERE r.show_on_public_org
			  AND r.latitude IS NOT NULL AND r.longitude IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
			                  WHERE e.org_id = $1 AND e.repeater_id = r.id))`, orgID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("has public repeater points: %w", err)
	}
	return exists, nil
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
		               WHERE e.org_id = o.id AND e.repeater_id = r.id),
		       EXISTS (SELECT 1 FROM org_repeater_command_optin oc
		               WHERE oc.org_id = o.id AND oc.repeater_id = r.id)
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
		err := r.Scan(&m.OrgID, &m.OrgSlug, &m.OrgName, &m.Excluded, &m.Restricted)
		return m, err
	})
}

// RepeaterConfigOrg is an org whose recommended configuration applies to a
// repeater: the repeater's owner is a member, the repeater isn't excluded, and
// the org has configuration (profiles and/or regions). Profiles lists that org's
// profile names (empty for a region-only org) for the console config picker.
type RepeaterConfigOrg struct {
	OrgID    int64    `json:"orgId"`
	OrgSlug  string   `json:"orgSlug"`
	OrgName  string   `json:"orgName"`
	Profiles []string `json:"profiles"`
}

// ListRepeaterConfigOrgs returns the orgs whose configuration applies to the
// repeater — those it participates in (owner is a member and it isn't excluded,
// the same predicate GetRepeaterForUser/CanSendCommand use) that have any config
// — each with its profile names. Ordered by org name. Used to populate the
// console's "Apply organization configuration" picker.
func (s *Store) ListRepeaterConfigOrgs(ctx context.Context, repeaterID int64) ([]RepeaterConfigOrg, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.slug, o.name,
		       COALESCE(
		           array_agg(p.name ORDER BY p.position, p.name) FILTER (WHERE p.id IS NOT NULL),
		           '{}') AS profiles
		FROM repeaters r
		JOIN org_members om ON om.user_id = r.owner_id
		JOIN organizations o ON o.id = om.org_id
		LEFT JOIN config_profiles p ON p.org_id = o.id
		WHERE r.id = $1
		  AND NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
		                  WHERE e.org_id = o.id AND e.repeater_id = r.id)
		  AND (EXISTS (SELECT 1 FROM config_profiles cp WHERE cp.org_id = o.id)
		       OR EXISTS (SELECT 1 FROM config_regions cr WHERE cr.org_id = o.id))
		GROUP BY o.id, o.slug, o.name
		ORDER BY o.name`, repeaterID)
	if err != nil {
		return nil, fmt.Errorf("list repeater config orgs: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (RepeaterConfigOrg, error) {
		var o RepeaterConfigOrg
		err := r.Scan(&o.OrgID, &o.OrgSlug, &o.OrgName, &o.Profiles)
		return o, err
	})
}
