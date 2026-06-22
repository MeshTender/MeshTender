package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/jleight/meshtender/internal/geo"
)

// ConfigStep is one ordered command line in a recommended-configuration profile.
// CommandID is the resolved catalog match (nil for a comment-only step).
type ConfigStep struct {
	Position    int
	CommandLine string
	CommandID   *int64
	Comment     string
}

// IsComment reports whether the step is a note rather than a runnable command.
func (s ConfigStep) IsComment() bool { return s.CommandLine == "" }

// Zone is a location zone read from a profile version: a geofence plus the steps
// that apply to repeaters inside it. Geofence is nil for a match-all zone.
type Zone struct {
	ID       int64
	Name     string
	Priority int
	Geofence *geo.Shape
	Steps    []ConfigStep
}

// ZoneInput is a zone as submitted by the editor for publishing. GeofenceJSON is
// raw GeoJSON (nil/empty = match-all); the storage column and resolver accept any
// polygon, so the rectangle-only v1 UI is not a model constraint.
type ZoneInput struct {
	Name         string
	Priority     int
	GeofenceJSON []byte
	Steps        []ConfigStep
}

// CurrentProfileVersion returns the org's latest config-profile version (id and
// number), or ErrNotFound when the org has not published a profile yet.
func (s *Store) CurrentProfileVersion(ctx context.Context, orgID int64) (id int64, version int, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT id, version FROM config_profile_versions WHERE org_id = $1 ORDER BY version DESC LIMIT 1`,
		orgID).Scan(&id, &version)
	if err != nil {
		return 0, 0, notFoundOr(err, "current config version")
	}
	return id, version, nil
}

// ProfileVersion reads a version's base steps (zone_id NULL) and its zones (each
// with their steps), ordered for stable rendering and resolution.
func (s *Store) ProfileVersion(ctx context.Context, versionID int64) (base []ConfigStep, zones []Zone, err error) {
	// Zones first, so we can attach steps by zone id.
	zrows, err := s.pool.Query(ctx,
		`SELECT id, name, priority, geofence FROM config_zones
		 WHERE version_id = $1 ORDER BY priority, id`, versionID)
	if err != nil {
		return nil, nil, fmt.Errorf("load zones: %w", err)
	}
	byID := map[int64]*Zone{}
	zones, err = collectRows(zrows, func(r pgx.Row) (Zone, error) {
		var z Zone
		var raw []byte
		if err := r.Scan(&z.ID, &z.Name, &z.Priority, &raw); err != nil {
			return Zone{}, err
		}
		if z.Geofence, err = geo.Parse(raw); err != nil {
			return Zone{}, err
		}
		return z, nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("scan zones: %w", err)
	}
	for i := range zones {
		byID[zones[i].ID] = &zones[i]
	}

	srows, err := s.pool.Query(ctx,
		`SELECT zone_id, position, command_line, command_id, comment
		 FROM config_profile_steps WHERE version_id = $1 ORDER BY position, id`, versionID)
	if err != nil {
		return nil, nil, fmt.Errorf("load steps: %w", err)
	}
	defer srows.Close()
	for srows.Next() {
		var zoneID *int64
		var st ConfigStep
		if err := srows.Scan(&zoneID, &st.Position, &st.CommandLine, &st.CommandID, &st.Comment); err != nil {
			return nil, nil, fmt.Errorf("scan step: %w", err)
		}
		if zoneID == nil {
			base = append(base, st)
		} else if z := byID[*zoneID]; z != nil {
			z.Steps = append(z.Steps, st)
		}
	}
	return base, zones, srows.Err()
}

// PublishProfileVersion creates the org's next profile version from base steps and
// zones (with their steps), returning the new version number. Mirrors
// PublishVersion's transactional shape.
func (s *Store) PublishProfileVersion(ctx context.Context, orgID int64, note string, createdBy int64, base []ConfigStep, zones []ZoneInput) (int, error) {
	var next int
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(max(version), 0) + 1 FROM config_profile_versions WHERE org_id = $1`,
			orgID).Scan(&next); err != nil {
			return fmt.Errorf("next config version: %w", err)
		}
		var versionID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO config_profile_versions (org_id, version, note, created_by)
			 VALUES ($1, $2, $3, $4) RETURNING id`,
			orgID, next, note, createdBy).Scan(&versionID); err != nil {
			return fmt.Errorf("insert config version: %w", err)
		}
		if err := insertSteps(ctx, tx, versionID, nil, base); err != nil {
			return fmt.Errorf("insert base steps: %w", err)
		}
		for _, z := range zones {
			var zoneID int64
			var geofence []byte // nil → SQL NULL → match-all
			if len(z.GeofenceJSON) > 0 {
				geofence = z.GeofenceJSON
			}
			if err := tx.QueryRow(ctx,
				`INSERT INTO config_zones (version_id, name, priority, geofence)
				 VALUES ($1, $2, $3, $4) RETURNING id`,
				versionID, z.Name, z.Priority, geofence).Scan(&zoneID); err != nil {
				return fmt.Errorf("insert zone %q: %w", z.Name, err)
			}
			if err := insertSteps(ctx, tx, versionID, &zoneID, z.Steps); err != nil {
				return fmt.Errorf("insert zone %q steps: %w", z.Name, err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}

// insertSteps writes an ordered run of steps for a version, optionally scoped to a
// zone (zoneID nil = base steps). Positions are assigned by slice order.
func insertSteps(ctx context.Context, tx pgx.Tx, versionID int64, zoneID *int64, steps []ConfigStep) error {
	for i, st := range steps {
		if _, err := tx.Exec(ctx,
			`INSERT INTO config_profile_steps (version_id, zone_id, position, command_line, command_id, comment)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			versionID, zoneID, i, st.CommandLine, st.CommandID, st.Comment); err != nil {
			return err
		}
	}
	return nil
}

// ResolveProfile produces the ordered command steps for a repeater at (lat, lon):
// the base steps, then every zone whose geofence contains the point, applied in
// (priority, id) order. Overlap is intentional — all matching zones contribute,
// including multiple at the same priority; nothing is dropped or deduped. When the
// location is unknown (nil), only match-all zones (nil geofence) are applied. It is
// a pure function so the resolution rules are unit-testable without a database.
func ResolveProfile(base []ConfigStep, zones []Zone, lat, lon *float64) []ConfigStep {
	out := append([]ConfigStep(nil), base...)

	matched := make([]Zone, 0, len(zones))
	for _, z := range zones {
		if z.Geofence == nil {
			matched = append(matched, z) // match-all zone always applies
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
	for _, z := range matched {
		out = append(out, z.Steps...)
	}
	return out
}
