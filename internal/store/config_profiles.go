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

// RegionMarker is the placeholder a profile step can hold on its own line to
// control where an org's region commands are spliced into the profile. Without
// it, region commands are appended after all profile steps (the default). It is
// never sent to a device — SplitAtRegionMarker removes it before either assembly
// path emits commands. It is stored as a step's CommandLine (CommandID nil), so
// it round-trips through the profile text editor like any other line.
const RegionMarker = "{{ region }}"

// IsRegionMarker reports whether the step is the region placeholder rather than a
// runnable command or a comment.
func (s ConfigStep) IsRegionMarker() bool { return s.CommandLine == RegionMarker }

// SplitAtRegionMarker partitions a profile's steps around the region marker: the
// steps before the first marker and the steps after it, with the marker itself
// (and any duplicates) dropped. When no marker is present every step lands in
// before and after is empty — so a caller that emits before, then the region
// commands, then after, appends the region block at the end (the default).
func SplitAtRegionMarker(steps []ConfigStep) (before, after []ConfigStep) {
	found := false
	for _, st := range steps {
		switch {
		case st.IsRegionMarker():
			found = true
		case found:
			after = append(after, st)
		default:
			before = append(before, st)
		}
	}
	return before, after
}

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
	Primary      bool // the org's primary region (frames the config preview map)
	AllowFlood   bool // whether flooding is allowed in this region (region allowf/denyf)
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
	Primary      bool
	AllowFlood   bool
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

// OrgProfileNames is an org paired with just the names of its config profiles,
// for pickers that need the names but not the full step bodies.
type OrgProfileNames struct {
	OrgID    int64
	OrgName  string
	Profiles []string
}

// ListOrgProfileNamesForUser returns every org the user belongs to together with
// that org's config-profile names, in a single query (avoids the per-org N+1 of
// calling ListProfiles in a loop). Orgs with no profiles are included with an
// empty Profiles slice. Rows are ordered to match ListOrgsForUser (org name, id)
// then profile display order (position, name), and are grouped by org here.
func (s *Store) ListOrgProfileNamesForUser(ctx context.Context, userID int64) ([]OrgProfileNames, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.name, p.name
		FROM org_members m
		JOIN organizations o ON o.id = m.org_id
		LEFT JOIN config_profiles p ON p.org_id = o.id
		WHERE m.user_id = $1
		ORDER BY lower(o.name), o.id, p.position, p.name`, userID)
	if err != nil {
		return nil, fmt.Errorf("list org profile names: %w", err)
	}
	defer rows.Close()

	// Rows for one org are contiguous (ordered by org first), so we can group by
	// index without a map — and index-based appends never dangle a pointer.
	var out []OrgProfileNames
	for rows.Next() {
		var orgID int64
		var orgName string
		var profileName *string // NULL for an org with no profiles (LEFT JOIN)
		if err := rows.Scan(&orgID, &orgName, &profileName); err != nil {
			return nil, fmt.Errorf("scan org profile name: %w", err)
		}
		if len(out) == 0 || out[len(out)-1].OrgID != orgID {
			out = append(out, OrgProfileNames{OrgID: orgID, OrgName: orgName})
		}
		if profileName != nil {
			last := &out[len(out)-1]
			last.Profiles = append(last.Profiles, *profileName)
		}
	}
	return out, rows.Err()
}

// ListRegions returns an org's regions ordered (layer, token) — i.e. root to leaf,
// the order their tokens appear in a `region def` chain.
func (s *Store) ListRegions(ctx context.Context, orgID int64) ([]Region, error) {
	rrows, err := s.pool.Query(ctx,
		`SELECT id, token, display_name, layer, is_primary, allow_flood, geofence FROM config_regions WHERE org_id = $1 ORDER BY layer, token`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list regions: %w", err)
	}
	regions, err := collectRows(rrows, func(r pgx.Row) (Region, error) {
		var z Region
		var raw []byte
		if err := r.Scan(&z.ID, &z.Token, &z.DisplayName, &z.Layer, &z.Primary, &z.AllowFlood, &raw); err != nil {
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
		if _, err := tx.Exec(ctx, `DELETE FROM config_profiles WHERE org_id = $1`, orgID); err != nil {
			return fmt.Errorf("clear profiles: %w", err)
		}
		for pos, p := range profiles {
			if _, err := insertProfile(ctx, tx, orgID, p.Name, pos, p.Steps); err != nil {
				return err
			}
		}
		return replaceRegionsTx(ctx, tx, orgID, regions)
	})
}

// ReplaceRegions replaces just an org's regions (profiles untouched) and sets the
// org's root (*) flood policy, in one transaction. Regions are spatially
// interdependent — parenting is derived from overlap — so the region editor sends
// the full desired set each save.
func (s *Store) ReplaceRegions(ctx context.Context, orgID int64, regions []RegionInput, rootAllowFlood bool) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE organizations SET root_allow_flood = $2 WHERE id = $1`, orgID, rootAllowFlood); err != nil {
			return fmt.Errorf("set root flood: %w", err)
		}
		return replaceRegionsTx(ctx, tx, orgID, regions)
	})
}

// RootAllowFlood reports whether flooding is allowed at the org's root region (*).
func (s *Store) RootAllowFlood(ctx context.Context, orgID int64) (bool, error) {
	var allow bool
	if err := s.pool.QueryRow(ctx,
		`SELECT root_allow_flood FROM organizations WHERE id = $1`, orgID).Scan(&allow); err != nil {
		return false, fmt.Errorf("root allow flood: %w", err)
	}
	return allow, nil
}

// CreateProfile inserts a new named profile for an org, appended after existing
// ones. Returns ErrDuplicate if the org already has a profile with that name.
func (s *Store) CreateProfile(ctx context.Context, orgID int64, name string, steps []ConfigStep) (int64, error) {
	var id int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var pos int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM config_profiles WHERE org_id = $1`, orgID).Scan(&pos); err != nil {
			return fmt.Errorf("count profiles: %w", err)
		}
		var err error
		id, err = insertProfile(ctx, tx, orgID, name, pos, steps)
		return err
	})
	if isUniqueViolation(err) {
		return 0, ErrDuplicate
	}
	return id, err
}

// GetProfile returns a single profile (with steps) scoped to its org, or
// ErrNotFound if no such profile belongs to the org.
func (s *Store) GetProfile(ctx context.Context, orgID, profileID int64) (*Profile, error) {
	var p Profile
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, position FROM config_profiles WHERE id = $1 AND org_id = $2`, profileID, orgID).
		Scan(&p.ID, &p.Name, &p.Position)
	if err != nil {
		return nil, notFoundOr(err, "get profile")
	}
	rows, err := s.pool.Query(ctx,
		`SELECT position, command_line, command_id, comment FROM config_profile_steps
		 WHERE profile_id = $1 ORDER BY position, id`, profileID)
	if err != nil {
		return nil, fmt.Errorf("get profile steps: %w", err)
	}
	p.Steps, err = collectRows(rows, func(r pgx.Row) (ConfigStep, error) {
		var st ConfigStep
		return st, r.Scan(&st.Position, &st.CommandLine, &st.CommandID, &st.Comment)
	})
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdateProfile renames a profile and replaces its steps, scoped to its org. Its
// position is preserved. Returns ErrNotFound if the profile isn't the org's, or
// ErrDuplicate if the new name collides with another profile.
func (s *Store) UpdateProfile(ctx context.Context, orgID, profileID int64, name string, steps []ConfigStep) error {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE config_profiles SET name = $3 WHERE id = $1 AND org_id = $2`, profileID, orgID, name)
		if err != nil {
			return fmt.Errorf("rename profile: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM config_profile_steps WHERE profile_id = $1`, profileID); err != nil {
			return fmt.Errorf("clear profile steps: %w", err)
		}
		return insertConfigSteps(ctx, tx, "config_profile_steps", "profile_id", profileID, steps)
	})
	if isUniqueViolation(err) {
		return ErrDuplicate
	}
	return err
}

// DeleteProfile removes a profile (its steps cascade), scoped to its org.
// Returns ErrNotFound if no such profile belongs to the org.
func (s *Store) DeleteProfile(ctx context.Context, orgID, profileID int64) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM config_profiles WHERE id = $1 AND org_id = $2`, profileID, orgID)
	if err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// insertProfile inserts one profile row plus its steps, returning the new id.
func insertProfile(ctx context.Context, tx pgx.Tx, orgID int64, name string, pos int, steps []ConfigStep) (int64, error) {
	var pid int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO config_profiles (org_id, name, position) VALUES ($1, $2, $3) RETURNING id`,
		orgID, name, pos).Scan(&pid); err != nil {
		return 0, fmt.Errorf("insert profile %q: %w", name, err)
	}
	if err := insertConfigSteps(ctx, tx, "config_profile_steps", "profile_id", pid, steps); err != nil {
		return 0, fmt.Errorf("insert profile %q steps: %w", name, err)
	}
	return pid, nil
}

// replaceRegionsTx clears an org's regions and inserts the given set within tx.
func replaceRegionsTx(ctx context.Context, tx pgx.Tx, orgID int64, regions []RegionInput) error {
	if _, err := tx.Exec(ctx, `DELETE FROM config_regions WHERE org_id = $1`, orgID); err != nil {
		return fmt.Errorf("clear regions: %w", err)
	}
	for _, z := range regions {
		var geofence []byte // nil → SQL NULL → everywhere
		if len(z.GeofenceJSON) > 0 {
			geofence = z.GeofenceJSON
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO config_regions (org_id, token, display_name, layer, is_primary, allow_flood, geofence) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			orgID, z.Token, z.DisplayName, z.Layer, z.Primary, z.AllowFlood, geofence); err != nil {
			return fmt.Errorf("insert region %q: %w", z.Token, err)
		}
	}
	return nil
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

// RegionDefCommands renders the regions that apply at (lat, lon) into the MeshCore
// commands to run on a repeater: a single `region def …` line describing the
// region tree for the location, then explicit flood policy for the root (*) and
// each applied region, then `region save`. The tree is the subset of regions whose
// geofence contains the point (match-all regions always apply), re-parented onto
// their nearest matching ancestor and serialized depth-first with the
// `child|ancestor` pop-back syntax so sibling branches (overlapping same-layer
// regions) come out correctly. Flood lines are always explicit (allowf/denyf) so
// applying the config normalizes the node over any prior manual settings.
//
// Returns nil when no region covers the location. This is the safety guard: a lone
// `region denyf *` with nothing allowed would kill all flooding, so the root deny
// only ever ships alongside a real def + at least one applied region.
func RegionDefCommands(regions []Region, rootAllowFlood bool, lat, lon *float64) []string {
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
	// Def line, then explicit flood policy (root first, then each applied region in
	// def order), then persist.
	out := []string{"region def " + strings.Join(tokens, " "), floodCommand("*", rootAllowFlood)}
	for _, node := range seq {
		out = append(out, floodCommand(regions[node].Token, regions[node].AllowFlood))
	}
	return append(out, "region save")
}

// floodCommand returns the MeshCore command setting a region's flood policy.
func floodCommand(name string, allow bool) string {
	if allow {
		return "region allowf " + name
	}
	return "region denyf " + name
}

// RegionMatches reports whether a region applies at (lat, lon): a match-all region
// always does; a geofenced one only when the location is known and inside it.
func RegionMatches(z Region, lat, lon *float64) bool {
	if z.Geofence == nil {
		return true
	}
	return lat != nil && lon != nil && z.Geofence.Contains(*lat, *lon)
}
