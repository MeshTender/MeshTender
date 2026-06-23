package web

import (
	"context"
	"errors"
	"net/http"
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
// member view (every participating repeater, with owner and links); false is the
// public view (only repeaters shown on the public org page, no links).
type RepeatersView struct {
	Repeaters []store.OrgRepeaterInfo
	HasMap    bool
	Full      bool
}

// BuildRepeatersView loads an org's repeaters for display. full selects the member
// view (all participating repeaters) vs the public view (public-page repeaters only).
func BuildRepeatersView(ctx context.Context, st *store.Store, orgID int64, full bool) (RepeatersView, error) {
	var reps []store.OrgRepeaterInfo
	var err error
	if full {
		reps, err = st.ListOrgRepeaters(ctx, orgID)
	} else {
		reps, err = st.ListPublicRepeaters(ctx, orgID)
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
