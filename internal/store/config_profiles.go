package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

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

// Region is one node in an org's region hierarchy. Token is the MeshCore region
// name used in `region def` (e.g. "buf"); DisplayName is the human label (e.g.
// "Buffalo"); Layer is its depth and ordering in the chain (lower = nearer the
// root). Geofence is nil for a region that applies everywhere. GeofenceJSON is the
// raw stored GeoJSON (nil for a match-all region), carried verbatim so the editor
// can round-trip an arbitrary polygon without collapsing it to its bounding box.
type Region struct {
	ID           int64
	Token        string
	DisplayName  string
	Layer        int
	Geofence     *geo.Shape
	GeofenceJSON []byte
}

// ProfileInput / RegionInput are an org's config as submitted by the editor for a
// full replace. GeofenceJSON is raw GeoJSON (nil/empty = everywhere).
type ProfileInput struct {
	Name  string
	Steps []ConfigStep
}
type RegionInput struct {
	Token        string
	DisplayName  string
	Layer        int
	GeofenceJSON []byte
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

// ListRegions returns an org's regions ordered (layer, token) — i.e. root to leaf,
// the order their tokens appear in a `region def` chain.
func (s *Store) ListRegions(ctx context.Context, orgID int64) ([]Region, error) {
	rrows, err := s.pool.Query(ctx,
		`SELECT id, token, display_name, layer, geofence FROM config_regions WHERE org_id = $1 ORDER BY layer, token`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list regions: %w", err)
	}
	regions, err := collectRows(rrows, func(r pgx.Row) (Region, error) {
		var z Region
		var raw []byte
		if err := r.Scan(&z.ID, &z.Token, &z.DisplayName, &z.Layer, &raw); err != nil {
			return Region{}, err
		}
		if z.Geofence, err = geo.Parse(raw); err != nil {
			return Region{}, err
		}
		z.GeofenceJSON = raw
		return z, nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan regions: %w", err)
	}
	return regions, nil
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
			if _, err := tx.Exec(ctx,
				`INSERT INTO config_regions (org_id, token, display_name, layer, geofence) VALUES ($1, $2, $3, $4, $5)`,
				orgID, z.Token, z.DisplayName, z.Layer, geofence); err != nil {
				return fmt.Errorf("insert region %q: %w", z.Token, err)
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

// regionParents returns, for each region, the index of its parent — the lower-
// layer region it overlaps most — or -1 for a root. Overlap is true intersection
// area, so a region nests under whichever ancestor it mostly sits in (e.g. a state
// that spills slightly across a border still nests under the country it covers
// most). Ties break toward the deeper layer, then the smaller token.
func regionParents(regions []Region) []int {
	parents := make([]int, len(regions))
	for i := range regions {
		parents[i] = -1
		var best float64
		for j := range regions {
			if i == j || regions[j].Layer >= regions[i].Layer {
				continue
			}
			area := regions[i].Geofence.OverlapArea(regions[j].Geofence)
			if area <= 0 {
				continue
			}
			if parents[i] == -1 || area > best || (area == best && betterParent(regions[j], regions[parents[i]])) {
				parents[i], best = j, area
			}
		}
	}
	return parents
}

// betterParent reports whether a is the preferred parent over b on an overlap tie:
// the deeper layer wins (closest ancestor), then the smaller token.
func betterParent(a, b Region) bool {
	if a.Layer != b.Layer {
		return a.Layer > b.Layer
	}
	return a.Token < b.Token
}

// RegionParentTokens returns each region's parent token ("" for a root), aligned
// with regions, using the same overlap-based parentage as the region def chain —
// for showing the derived hierarchy in the editor/read-only views.
func RegionParentTokens(regions []Region) []string {
	parents := regionParents(regions)
	out := make([]string, len(regions))
	for i, p := range parents {
		if p != -1 {
			out[i] = regions[p].Token
		}
	}
	return out
}

// RegionDefCommands renders the regions that apply at (lat, lon) into the MeshCore
// commands to run on a repeater: a single `region def …` line describing the
// region tree for the location, followed by `region save`. The tree is the subset
// of regions whose geofence contains the point (match-all regions always apply),
// re-parented onto their nearest matching ancestor and serialized depth-first with
// the `child|ancestor` pop-back syntax so sibling branches (overlapping same-layer
// regions) come out correctly. Returns nil when no region covers the location.
func RegionDefCommands(regions []Region, lat, lon *float64) []string {
	parents := regionParents(regions)
	matched := make([]bool, len(regions))
	any := false
	for i, z := range regions {
		if RegionMatches(z, lat, lon) {
			matched[i], any = true, true
		}
	}
	if !any {
		return nil
	}
	// Collapse unmatched intermediate ancestors: a matched region hangs off its
	// nearest matched ancestor, or the root (*) if none match.
	children := make(map[int][]int)
	var roots []int
	for i := range regions {
		if !matched[i] {
			continue
		}
		p := parents[i]
		for p != -1 && !matched[p] {
			p = parents[p]
		}
		if p == -1 {
			roots = append(roots, i)
		} else {
			children[p] = append(children[p], i)
		}
	}
	byLayerToken := func(idx []int) {
		sort.SliceStable(idx, func(a, b int) bool {
			if regions[idx[a]].Layer != regions[idx[b]].Layer {
				return regions[idx[a]].Layer < regions[idx[b]].Layer
			}
			return regions[idx[a]].Token < regions[idx[b]].Token
		})
	}
	byLayerToken(roots)
	for _, c := range children {
		byLayerToken(c)
	}

	// Depth-first pre-order, popping the cursor with |ancestor whenever the next
	// node isn't a child of the one just emitted.
	var seq []int
	var walk func(n int)
	walk = func(n int) {
		seq = append(seq, n)
		for _, c := range children[n] {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	effParent := func(n int) int {
		p := parents[n]
		for p != -1 && !matched[p] {
			p = parents[p]
		}
		return p
	}
	var tokens []string
	for k, node := range seq {
		if k > 0 {
			parent := effParent(node)
			if parent != seq[k-1] { // not a child of the previous node → pop the cursor
				jump := "*"
				if parent != -1 {
					jump = regions[parent].Token
				}
				tokens[len(tokens)-1] += "|" + jump
			}
		}
		tokens = append(tokens, regions[node].Token)
	}
	return []string{"region def " + strings.Join(tokens, " "), "region save"}
}

// RegionMatches reports whether a region applies at (lat, lon): a match-all region
// always does; a geofenced one only when the location is known and inside it.
func RegionMatches(z Region, lat, lon *float64) bool {
	if z.Geofence == nil {
		return true
	}
	return lat != nil && lon != nil && z.Geofence.Contains(*lat, *lon)
}
