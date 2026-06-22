package web

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/jleight/meshtender/internal/store"
)

// This file builds the read-only org "Configuration" and "Requested access"
// views. They live here (not in core) so both the app host (signed-in) and the
// root/marketing host (anonymous) can render the same shared templates — the
// same split that lets both surfaces render org_public.html.

// ZoneView is a profile zone rendered read-only: the geofence reduced to its
// bounding box as display strings (empty when the zone matches everywhere).
type ZoneView struct {
	Name     string
	Priority int
	MatchAll bool
	MinLat   string
	MinLon   string
	MaxLat   string
	MaxLon   string
	Steps    []store.ConfigStep
}

// ConfigView is an org's recommended configuration for display. Preview holds
// the commands resolved for the optional preview location (nil when no location
// was requested or no profile exists).
type ConfigView struct {
	HasProfile bool
	Version    int
	BaseSteps  []store.ConfigStep
	Zones      []ZoneView
	Preview    []store.ConfigStep
}

// BuildConfigView loads the org's current published config profile for read-only
// display. An org with no published profile yields HasProfile=false (not an error).
// When lat/lon are non-nil, Preview is the command list resolved for that location.
func BuildConfigView(ctx context.Context, st *store.Store, orgID int64, lat, lon *float64) (ConfigView, error) {
	vid, version, err := st.CurrentProfileVersion(ctx, orgID)
	if errors.Is(err, store.ErrNotFound) {
		return ConfigView{}, nil
	}
	if err != nil {
		return ConfigView{}, err
	}
	base, zones, err := st.ProfileVersion(ctx, vid)
	if err != nil {
		return ConfigView{}, err
	}
	cv := ConfigView{HasProfile: true, Version: version, BaseSteps: base}
	if lat != nil && lon != nil {
		cv.Preview = store.ResolveProfile(base, zones, lat, lon)
	}
	for _, z := range zones {
		zv := ZoneView{Name: z.Name, Priority: z.Priority, Steps: z.Steps}
		if minLat, minLon, maxLat, maxLon, ok := z.Geofence.Bounds(); ok {
			zv.MinLat = formatCoord(minLat)
			zv.MinLon = formatCoord(minLon)
			zv.MaxLat = formatCoord(maxLat)
			zv.MaxLon = formatCoord(maxLon)
		} else {
			zv.MatchAll = true
		}
		cv.Zones = append(cv.Zones, zv)
	}
	return cv, nil
}

func formatCoord(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// OrgNav is the data the shared org-tabs sub-nav partial expects: the org slug,
// which tab is active ("home" | "repeaters" | "members" | "config" |
// "permissions"), and whether the viewer is a member (the Members tab, which
// exposes personal info, only shows for members).
func OrgNav(slug, active string, isMember bool) map[string]any {
	return map[string]any{"Slug": slug, "Active": active, "IsMember": isMember}
}

// RepeatersView is an org's repeaters for the Repeaters page. Full is true for the
// member view (every contributed repeater, with owner and links); false is the
// public view (only repeaters opted into the public map, no links).
type RepeatersView struct {
	Repeaters []store.OrgRepeaterInfo
	HasMap    bool
	Full      bool
}

// BuildRepeatersView loads an org's repeaters for display. full selects the member
// view (all contributed repeaters) vs the public view (public-map repeaters only).
func BuildRepeatersView(ctx context.Context, st *store.Store, orgID int64, full bool) (RepeatersView, error) {
	var reps []store.OrgRepeaterInfo
	var err error
	if full {
		reps, err = st.ListOrgRepeaters(ctx, orgID)
	} else {
		reps, err = st.ListPublicMapRepeaters(ctx, orgID)
	}
	if err != nil {
		return RepeatersView{}, err
	}
	hasMap := false
	for _, rp := range reps {
		if rp.HasLocation {
			hasMap = true
			break
		}
	}
	return RepeatersView{Repeaters: reps, HasMap: hasMap, Full: full}, nil
}

// PreviewLatLon parses the optional ?lat=&lon= config-preview coordinates.
func PreviewLatLon(r *http.Request) (lat, lon float64, ok bool) {
	ls, ns := r.URL.Query().Get("lat"), r.URL.Query().Get("lon")
	if ls == "" || ns == "" {
		return 0, 0, false
	}
	var err1, err2 error
	lat, err1 = strconv.ParseFloat(ls, 64)
	lon, err2 = strconv.ParseFloat(ns, 64)
	return lat, lon, err1 == nil && err2 == nil
}

// CmdCell is one command in a feature-table cell (the "feature-table" partial in
// base.html renders .Template/.Description/.Risky).
type CmdCell struct {
	Template    string
	Description string // shown as a hover tooltip
	Risky       bool
}

// FeatureRow is one feature's allowed commands bucketed by operation, for the
// read-only feature×operation tables (consent, requested-access, contribute).
type FeatureRow struct {
	Feature                     string
	Read, Write, Delete, Action []CmdCell
}

// FeatureTableFor groups the commands in `allowed` (the id-set a single tier may
// run) by feature × operation, ordered by featureOrder — one table per tier. This
// is the canonical home for the consent/permission feature table so both the app
// host and the root host (which can't import core) can build it.
func FeatureTableFor(catalog []*store.Command, allowed map[int64]bool) []FeatureRow {
	byFeature := map[string]*FeatureRow{}
	var present []string
	for _, c := range catalog {
		if !allowed[c.ID] {
			continue
		}
		row := byFeature[c.Feature]
		if row == nil {
			row = &FeatureRow{Feature: c.Feature}
			byFeature[c.Feature] = row
			present = append(present, c.Feature)
		}
		cell := CmdCell{Template: c.Template, Description: c.Description, Risky: c.Risky}
		switch c.Operation {
		case "read":
			row.Read = append(row.Read, cell)
		case "delete":
			row.Delete = append(row.Delete, cell)
		case "action":
			row.Action = append(row.Action, cell)
		default: // "write"
			row.Write = append(row.Write, cell)
		}
	}
	orderFeatures(present)
	out := make([]FeatureRow, 0, len(present))
	for _, f := range present {
		out = append(out, *byFeature[f])
	}
	return out
}

// PermissionsView is an org's current requested-access policy for display: one
// feature×operation table per tier. Admins inherit every member command, so the
// admin table is member ∪ admin.
type PermissionsView struct {
	Version        int
	MemberFeatures []FeatureRow
	AdminFeatures  []FeatureRow
	HasRisky       bool // any granted command is risky (drives the warning copy)
}

// BuildPermissionsView loads the org's current permission version and builds the
// member and admin feature tables.
func BuildPermissionsView(ctx context.Context, st *store.Store, orgID int64) (PermissionsView, error) {
	versionID, version, err := st.CurrentVersion(ctx, orgID)
	if err != nil {
		return PermissionsView{}, err
	}
	adminIDs, memberIDs, err := st.VersionCommandIDs(ctx, versionID)
	if err != nil {
		return PermissionsView{}, err
	}
	catalog, err := st.ListCommands(ctx)
	if err != nil {
		return PermissionsView{}, err
	}
	member := idSet(memberIDs)
	adminUnion := idSet(adminIDs)
	for id := range member {
		adminUnion[id] = true
	}
	pv := PermissionsView{
		Version:        version,
		MemberFeatures: FeatureTableFor(catalog, member),
		AdminFeatures:  FeatureTableFor(catalog, adminUnion),
	}
	for _, c := range catalog {
		if c.Risky && adminUnion[c.ID] {
			pv.HasRisky = true
			break
		}
	}
	return pv, nil
}

func idSet(ids []int64) map[int64]bool {
	m := make(map[int64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// featureOrder mirrors core's command_features.go display order so the public
// permissions view groups features the same way the in-app review/editor does.
// Unknown features sort after these, alphabetically.
var featureOrder = []string{"Radio", "Routing", "Advertising", "Location", "GPS", "Clock",
	"Region", "Neighbors", "Sensors", "Identity", "Access", "Power", "Diagnostics", "Firmware"}

func featureRank(f string) int {
	for i, x := range featureOrder {
		if x == f {
			return i
		}
	}
	return len(featureOrder)
}

func orderFeatures(present []string) {
	sort.SliceStable(present, func(i, j int) bool {
		ri, rj := featureRank(present[i]), featureRank(present[j])
		if ri != rj {
			return ri < rj
		}
		return present[i] < present[j]
	})
}
