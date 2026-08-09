package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/MeshTender/MeshTender/internal/geo"
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
// root). Geofence is nil for a draft — a region whose area hasn't been drawn yet,
// which applies nowhere (see RegionMatches). GeofenceJSON is the raw stored GeoJSON,
// carried verbatim so the editor can round-trip an arbitrary polygon without
// collapsing it to its bounding box.
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

// ProfileInput / RegionInput are an org's config as submitted by the editor.
// GeofenceJSON is raw GeoJSON; nil/empty leaves the region a draft (applies
// nowhere) until an area is drawn.
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

// Regions are edited one at a time (attributes in a modal, geometry on the area
// page), so each of the following touches exactly one row. Contrast
// ReplaceOrgConfig above, which still replaces an org's whole config wholesale for
// seeding and imports.

// CreateRegion inserts one region for an org and returns its id. GeofenceJSON may
// be nil — that's a draft region whose area hasn't been drawn yet, which applies
// nowhere until it has a shape (see RegionMatches). Returns ErrDuplicate if the org
// already has a region with that token.
func (s *Store) CreateRegion(ctx context.Context, orgID int64, z RegionInput) (int64, error) {
	var id int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := clearOtherPrimaries(ctx, tx, orgID, 0, z.Primary); err != nil {
			return err
		}
		return tx.QueryRow(ctx,
			`INSERT INTO config_regions (org_id, token, display_name, layer, is_primary, allow_flood, geofence)
			 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			orgID, z.Token, z.DisplayName, z.Layer, z.Primary, z.AllowFlood, nilIfEmpty(z.GeofenceJSON)).Scan(&id)
	})
	if isUniqueViolation(err) {
		return 0, ErrDuplicate
	}
	if err != nil {
		return 0, fmt.Errorf("insert region %q: %w", z.Token, err)
	}
	return id, nil
}

// GetRegion returns a single region scoped to its org, or ErrNotFound if no such
// region belongs to the org.
func (s *Store) GetRegion(ctx context.Context, orgID, regionID int64) (*Region, error) {
	var z Region
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT id, token, display_name, layer, is_primary, allow_flood, geofence
		 FROM config_regions WHERE id = $1 AND org_id = $2`, regionID, orgID).
		Scan(&z.ID, &z.Token, &z.DisplayName, &z.Layer, &z.Primary, &z.AllowFlood, &raw)
	if err != nil {
		return nil, notFoundOr(err, "get region")
	}
	if z.Geofence, err = geo.Parse(raw); err != nil {
		return nil, fmt.Errorf("get region %d: %w", regionID, err)
	}
	z.GeofenceJSON = raw
	return &z, nil
}

// UpdateRegion replaces a region's attributes, leaving its geofence alone — the
// area is saved separately by UpdateRegionGeofence, so a failed attribute edit can
// never discard a drawn shape. Returns ErrNotFound if the region isn't the org's,
// or ErrDuplicate if the new token collides with another of its regions.
func (s *Store) UpdateRegion(ctx context.Context, orgID, regionID int64, z RegionInput) error {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := clearOtherPrimaries(ctx, tx, orgID, regionID, z.Primary); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE config_regions SET token = $3, display_name = $4, layer = $5, is_primary = $6, allow_flood = $7
			 WHERE id = $1 AND org_id = $2`,
			regionID, orgID, z.Token, z.DisplayName, z.Layer, z.Primary, z.AllowFlood)
		if err != nil {
			return fmt.Errorf("update region: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
	if isUniqueViolation(err) {
		return ErrDuplicate
	}
	return err
}

// UpdateRegionGeofence saves just a region's drawn area. A nil/empty geofence
// clears it back to a draft. Returns ErrNotFound if the region isn't the org's.
func (s *Store) UpdateRegionGeofence(ctx context.Context, orgID, regionID int64, geofence []byte) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE config_regions SET geofence = $3 WHERE id = $1 AND org_id = $2`,
		regionID, orgID, nilIfEmpty(geofence))
	if err != nil {
		return fmt.Errorf("update region geofence: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteRegion removes a region, scoped to its org. Returns ErrNotFound if no such
// region belongs to the org.
func (s *Store) DeleteRegion(ctx context.Context, orgID, regionID int64) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM config_regions WHERE id = $1 AND org_id = $2`, regionID, orgID)
	if err != nil {
		return fmt.Errorf("delete region: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetRootAllowFlood sets the org's root (*) flood policy on its own — the root
// isn't a config_regions row, so it's toggled independently of any region.
func (s *Store) SetRootAllowFlood(ctx context.Context, orgID int64, allow bool) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE organizations SET root_allow_flood = $2 WHERE id = $1`, orgID, allow); err != nil {
		return fmt.Errorf("set root flood: %w", err)
	}
	return nil
}

// clearOtherPrimaries demotes the org's other primary regions so the one being
// written can take the flag. keepID is excluded (0 when inserting). A no-op when
// the region isn't becoming primary — turning the flag off never touches siblings.
// This runs before the write, so config_regions_one_primary_idx never actually
// fires; the index is there to keep the invariant if anything else writes the table.
func clearOtherPrimaries(ctx context.Context, tx pgx.Tx, orgID, keepID int64, primary bool) error {
	if !primary {
		return nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE config_regions SET is_primary = false WHERE org_id = $1 AND id <> $2 AND is_primary`,
		orgID, keepID); err != nil {
		return fmt.Errorf("clear other primary regions: %w", err)
	}
	return nil
}

// nilIfEmpty maps an empty geofence to nil so it lands as SQL NULL (a draft region)
// rather than an empty JSONB value.
func nilIfEmpty(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
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
		if _, err := tx.Exec(ctx,
			`INSERT INTO config_regions (org_id, token, display_name, layer, is_primary, allow_flood, geofence) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			orgID, z.Token, z.DisplayName, z.Layer, z.Primary, z.AllowFlood, nilIfEmpty(z.GeofenceJSON)); err != nil {
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

// RegionMatches reports whether a region applies at (lat, lon): only when the
// location is known and inside the region's geofence.
//
// A region with no geofence applies *nowhere*, not everywhere. Every row in
// config_regions is a bounded area by definition — the only "applies everywhere"
// region is the org root (*), which lives on the organizations row and is never a
// region row. So a NULL geofence means a draft: a region created before its area
// has been drawn. It stays out of every `region def` chain until it has a shape,
// rather than silently applying to every repeater on the mesh.
func RegionMatches(z Region, lat, lon *float64) bool {
	if z.Geofence == nil {
		return false
	}
	return lat != nil && lon != nil && z.Geofence.Contains(*lat, *lon)
}
