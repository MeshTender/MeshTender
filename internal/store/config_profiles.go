package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/jleight/meshtender/internal/geo"
)

// An org's configuration is split into two independent parts (see
// docs/auth-cross-host.md's sibling design notes):
//
//   - Profiles: named, mutable sets of base-setting command steps (e.g. one per
//     board family). To configure a repeater you pick a profile for its base
//     settings.
//   - Regions: org-level geofenced steps applied purely by location. They do not
//     depend on which profile is chosen — the two never interact.

// ConfigStep is one ordered command line. CommandID is the resolved catalog match
// (nil for a comment-only step).
type ConfigStep struct {
	Position    int
	CommandLine string
	CommandID   *int64
	Comment     string
}

// IsComment reports whether the step is a note rather than a runnable command.
func (s ConfigStep) IsComment() bool { return s.CommandLine == "" }

// Profile is a named set of base-setting steps.
type Profile struct {
	ID       int64
	Name     string
	Position int
	Steps    []ConfigStep
}

// Region is an org-level geofenced set of location steps. Geofence is nil for a
// region that applies everywhere.
type Region struct {
	ID       int64
	Name     string
	Priority int
	Geofence *geo.Shape
	Steps    []ConfigStep
}

// ProfileInput / RegionInput are an org's config as submitted by the editor for a
// full replace. GeofenceJSON is raw GeoJSON (nil/empty = everywhere).
type ProfileInput struct {
	Name  string
	Steps []ConfigStep
}
type RegionInput struct {
	Name         string
	Priority     int
	GeofenceJSON []byte
	Steps        []ConfigStep
}

// OrgHasConfig reports whether an org has any profile or region defined (drives
// hiding the Configuration tab when there's nothing to show).
func (s *Store) OrgHasConfig(ctx context.Context, orgID int64) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM config_profiles WHERE org_id = $1)
		    OR EXISTS (SELECT 1 FROM config_regions  WHERE org_id = $1)`, orgID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("org has config: %w", err)
	}
	return exists, nil
}

// ListProfiles returns an org's named profiles with their steps, ordered for
// stable display (position, then name).
func (s *Store) ListProfiles(ctx context.Context, orgID int64) ([]Profile, error) {
	prows, err := s.pool.Query(ctx,
		`SELECT id, name, position FROM config_profiles WHERE org_id = $1 ORDER BY position, name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	profiles, err := collectRows(prows, func(r pgx.Row) (Profile, error) {
		var p Profile
		return p, r.Scan(&p.ID, &p.Name, &p.Position)
	})
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*Profile, len(profiles))
	for i := range profiles {
		byID[profiles[i].ID] = &profiles[i]
	}
	srows, err := s.pool.Query(ctx, `
		SELECT s.profile_id, s.position, s.command_line, s.command_id, s.comment
		FROM config_profile_steps s
		JOIN config_profiles p ON p.id = s.profile_id
		WHERE p.org_id = $1 ORDER BY s.position, s.id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list profile steps: %w", err)
	}
	defer srows.Close()
	for srows.Next() {
		var pid int64
		var st ConfigStep
		if err := srows.Scan(&pid, &st.Position, &st.CommandLine, &st.CommandID, &st.Comment); err != nil {
			return nil, fmt.Errorf("scan profile step: %w", err)
		}
		if p := byID[pid]; p != nil {
			p.Steps = append(p.Steps, st)
		}
	}
	return profiles, srows.Err()
}

// ListRegions returns an org's regions with their steps, ordered (priority, id).
func (s *Store) ListRegions(ctx context.Context, orgID int64) ([]Region, error) {
	rrows, err := s.pool.Query(ctx,
		`SELECT id, name, priority, geofence FROM config_regions WHERE org_id = $1 ORDER BY priority, id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list regions: %w", err)
	}
	regions, err := collectRows(rrows, func(r pgx.Row) (Region, error) {
		var z Region
		var raw []byte
		if err := r.Scan(&z.ID, &z.Name, &z.Priority, &raw); err != nil {
			return Region{}, err
		}
		if z.Geofence, err = geo.Parse(raw); err != nil {
			return Region{}, err
		}
		return z, nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan regions: %w", err)
	}
	byID := make(map[int64]*Region, len(regions))
	for i := range regions {
		byID[regions[i].ID] = &regions[i]
	}
	srows, err := s.pool.Query(ctx, `
		SELECT s.region_id, s.position, s.command_line, s.command_id, s.comment
		FROM config_region_steps s
		JOIN config_regions z ON z.id = s.region_id
		WHERE z.org_id = $1 ORDER BY s.position, s.id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list region steps: %w", err)
	}
	defer srows.Close()
	for srows.Next() {
		var rid int64
		var st ConfigStep
		if err := srows.Scan(&rid, &st.Position, &st.CommandLine, &st.CommandID, &st.Comment); err != nil {
			return nil, fmt.Errorf("scan region step: %w", err)
		}
		if z := byID[rid]; z != nil {
			z.Steps = append(z.Steps, st)
		}
	}
	return regions, srows.Err()
}

// ReplaceOrgConfig replaces an org's entire config (all profiles + regions) with
// the submitted set, in one transaction. Profiles are mutable — there is no
// version history — so the editor sends the full desired state each save.
func (s *Store) ReplaceOrgConfig(ctx context.Context, orgID int64, profiles []ProfileInput, regions []RegionInput) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		// Steps cascade from their profile/region, so deleting the parents is enough.
		if _, err := tx.Exec(ctx, `DELETE FROM config_profiles WHERE org_id = $1`, orgID); err != nil {
			return fmt.Errorf("clear profiles: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM config_regions WHERE org_id = $1`, orgID); err != nil {
			return fmt.Errorf("clear regions: %w", err)
		}
		for pos, p := range profiles {
			var pid int64
			if err := tx.QueryRow(ctx,
				`INSERT INTO config_profiles (org_id, name, position) VALUES ($1, $2, $3) RETURNING id`,
				orgID, p.Name, pos).Scan(&pid); err != nil {
				return fmt.Errorf("insert profile %q: %w", p.Name, err)
			}
			if err := insertConfigSteps(ctx, tx, "config_profile_steps", "profile_id", pid, p.Steps); err != nil {
				return fmt.Errorf("insert profile %q steps: %w", p.Name, err)
			}
		}
		for _, z := range regions {
			var geofence []byte // nil → SQL NULL → everywhere
			if len(z.GeofenceJSON) > 0 {
				geofence = z.GeofenceJSON
			}
			var rid int64
			if err := tx.QueryRow(ctx,
				`INSERT INTO config_regions (org_id, name, priority, geofence) VALUES ($1, $2, $3, $4) RETURNING id`,
				orgID, z.Name, z.Priority, geofence).Scan(&rid); err != nil {
				return fmt.Errorf("insert region %q: %w", z.Name, err)
			}
			if err := insertConfigSteps(ctx, tx, "config_region_steps", "region_id", rid, z.Steps); err != nil {
				return fmt.Errorf("insert region %q steps: %w", z.Name, err)
			}
		}
		return nil
	})
}

// insertConfigSteps writes an ordered run of steps into a step table keyed by a
// parent id column. table/parentCol are fixed internal constants — never user
// input — so the interpolation is safe.
func insertConfigSteps(ctx context.Context, tx pgx.Tx, table, parentCol string, parentID int64, steps []ConfigStep) error {
	q := fmt.Sprintf(
		`INSERT INTO %s (%s, position, command_line, command_id, comment) VALUES ($1, $2, $3, $4, $5)`,
		table, parentCol)
	for i, st := range steps {
		if _, err := tx.Exec(ctx, q, parentID, i, st.CommandLine, st.CommandID, st.Comment); err != nil {
			return err
		}
	}
	return nil
}

// ResolveRegions returns the region steps that apply at (lat, lon): every region
// whose geofence contains the point (plus match-all regions), in (priority, id)
// order. Overlap is intentional — all matching regions contribute. With an
// unknown location (nil), only match-all regions apply. Pure, for unit testing.
func ResolveRegions(regions []Region, lat, lon *float64) []ConfigStep {
	matched := make([]Region, 0, len(regions))
	for _, z := range regions {
		if z.Geofence == nil {
			matched = append(matched, z)
			continue
		}
		if lat != nil && lon != nil && z.Geofence.Contains(*lat, *lon) {
			matched = append(matched, z)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].Priority != matched[j].Priority {
			return matched[i].Priority < matched[j].Priority
		}
		return matched[i].ID < matched[j].ID
	})
	var out []ConfigStep
	for _, z := range matched {
		out = append(out, z.Steps...)
	}
	return out
}

// RegionMatches reports whether a region applies at (lat, lon): a match-all region
// always does; a geofenced one only when the location is known and inside it.
func RegionMatches(z Region, lat, lon *float64) bool {
	if z.Geofence == nil {
		return true
	}
	return lat != nil && lon != nil && z.Geofence.Contains(*lat, *lon)
}
