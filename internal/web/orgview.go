package web

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jleight/meshtender/internal/store"
)

// This file builds the read-only org "Configuration" and "Requested access"
// views. They live here (not in core) so both the app host (signed-in) and the
// root/marketing host (anonymous) can render the same shared templates — the
// same split that lets both surfaces render org_public.html.

// ProfileView is one named base-settings profile for display. ID is carried so
// the admin surface can build its edit/delete URLs from the same list the
// read-only view renders (the public surface just ignores it).
type ProfileView struct {
	ID    int64
	Name  string
	Steps []store.ConfigStep
}

// ConfigView is an org's configuration for display: its named profiles (with one
// selected for its base settings) and its regions, kept independent. The regions
// themselves aren't listed here — they surface only through the location picker —
// so this carries just what that map needs. PreviewActive is true when a location
// was supplied.
type ConfigView struct {
	HasConfig       bool
	Profiles        []ProfileView
	Selected        string
	SelectedSteps   []store.ConfigStep
	HasRegions      bool      // org defines at least one region
	HasRegionShapes bool      // some region has a geofence (so the picker map is useful)
	MapBounds       []float64 // {minLat, minLon, maxLat, maxLon} framing all geofences, or nil
	RegionDef       []string  // region def/save commands for the previewed location
	PreviewActive   bool
}

// bbox accumulates a lat/lon bounding box. A nil *bbox is empty, so extend can be
// chained from nil to fold in the first point/box.
type bbox struct{ minLat, minLon, maxLat, maxLon float64 }

func (b *bbox) extend(minLat, minLon, maxLat, maxLon float64) *bbox {
	if b == nil {
		return &bbox{minLat, minLon, maxLat, maxLon}
	}
	b.minLat, b.minLon = min(b.minLat, minLat), min(b.minLon, minLon)
	b.maxLat, b.maxLon = max(b.maxLat, maxLat), max(b.maxLon, maxLon)
	return b
}

// BuildConfigView loads an org's profiles and regions for read-only display.
// selected names the profile whose base settings to show (falls back to the
// first). lat/lon, when non-nil, mark which regions apply at that location. An
// org with neither profiles nor regions yields HasConfig=false (not an error).
func BuildConfigView(ctx context.Context, st *store.Store, orgID int64, selected string, lat, lon *float64) (ConfigView, error) {
	profiles, err := st.ListProfiles(ctx, orgID)
	if err != nil {
		return ConfigView{}, err
	}
	regions, err := st.ListRegions(ctx, orgID)
	if err != nil {
		return ConfigView{}, err
	}
	cv := ConfigView{
		HasConfig:     len(profiles) > 0 || len(regions) > 0,
		HasRegions:    len(regions) > 0,
		PreviewActive: lat != nil && lon != nil,
	}
	for _, p := range profiles {
		cv.Profiles = append(cv.Profiles, ProfileView{ID: p.ID, Name: p.Name, Steps: p.Steps})
	}
	if len(profiles) > 0 {
		idx := 0
		for i, p := range profiles {
			if p.Name == selected {
				idx = i
				break
			}
		}
		cv.Selected = profiles[idx].Name
		cv.SelectedSteps = profiles[idx].Steps
	}
	// Frame the location-preview map. Prefer the primary region's geofence; if
	// none is set (or it has no shape), fall back to the org's public repeaters;
	// failing that, the union of all geofences. (The picker only renders when
	// HasRegionShapes, so a usable box almost always exists.)
	var union *bbox
	for _, z := range regions {
		if len(z.GeofenceJSON) > 0 {
			cv.HasRegionShapes = true
		}
		if a, b, c, d, ok := z.Geofence.Bounds(); ok {
			union = union.extend(a, b, c, d)
			if z.Primary {
				cv.MapBounds = []float64{a, b, c, d}
			}
		}
	}
	if cv.MapBounds == nil {
		if reps, err := st.ListPublicRepeaters(ctx, orgID); err == nil {
			var rb *bbox
			for _, rp := range reps {
				if rp.HasLocation {
					rb = rb.extend(rp.Lat, rp.Lon, rp.Lat, rp.Lon)
				}
			}
			if rb != nil {
				cv.MapBounds = []float64{rb.minLat, rb.minLon, rb.maxLat, rb.maxLon}
			}
		}
	}
	if cv.MapBounds == nil && union != nil {
		cv.MapBounds = []float64{union.minLat, union.minLon, union.maxLat, union.maxLon}
	}
	if cv.PreviewActive {
		rootAllow, err := st.RootAllowFlood(ctx, orgID)
		if err != nil {
			return ConfigView{}, err
		}
		cv.RegionDef = store.RegionDefCommands(regions, rootAllow, lat, lon)
	}
	return cv, nil
}

// OrgNavArgs is the input to OrgNavFor and the data the shared org-header +
// org-tabs partials expect.
type OrgNavArgs struct {
	OrgID  int64
	Name   string
	Slug   string
	Active string // "home" | "members" | "repeaters" | "config"
	// IsMember gates the Members tab (personal info), so it's false on public views
	// even when a member is previewing. IsAdmin gates the "Edit configuration"
	// action.
	IsMember bool
	IsAdmin  bool
	// Manage selects the header's right-side controls: true renders the member
	// Actions dropdown, false renders the public "Go to organization / Join" CTA.
	Manage bool
	// CanGoToOrg / CanJoin drive that CTA (used only when !Manage): a member
	// previewing the public page gets "Go to organization", a signed-in non-member
	// gets "Join organization", and anyone else gets "Sign in to join".
	CanGoToOrg bool
	CanJoin    bool
}

// OrgNavFor builds the org nav map, querying whether the org has any config so the
// Configuration tab hides when empty — for everyone except admins, who always see
// it so they can create the first profile/region.
func (e *Env) OrgNavFor(ctx context.Context, a OrgNavArgs) map[string]any {
	hasConfig, _ := e.Store.OrgHasConfig(ctx, a.OrgID)
	return map[string]any{
		"Name": a.Name, "Slug": a.Slug, "Active": a.Active,
		"IsMember": a.IsMember, "IsAdmin": a.IsAdmin,
		"ShowConfig": hasConfig || a.IsAdmin,
		"Manage":     a.Manage, "CanGoToOrg": a.CanGoToOrg, "CanJoin": a.CanJoin,
	}
}

// RepeatersView is an org's repeaters for the Repeaters page. Full is true for the
// member view (every participating repeater, with owner and links); false is the
// public view (only repeaters shown on the public org page, no links). MapPoints
// is the full located set for the map (independent of any list paging), and
// NextCursor, when set, drives the public list's "show more" control.
type RepeatersView struct {
	Repeaters  []store.OrgRepeaterInfo
	MapPoints  []store.MapPoint
	HasMap     bool
	Full       bool
	NextCursor string
}

// BuildRepeatersView loads an org's repeaters for display. full selects the member
// view (all participating repeaters) vs the public view (public-page repeaters
// only). It returns the complete set (no paging); the marketing surface paginates
// the public list separately via BuildPublicRepeatersPage.
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
	var points []store.MapPoint
	for _, rp := range reps {
		if rp.HasLocation {
			points = append(points, store.MapPoint{Name: rp.Name, Lat: rp.Lat, Lon: rp.Lon})
		}
	}
	return RepeatersView{Repeaters: reps, MapPoints: points, HasMap: len(points) > 0, Full: full}, nil
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
