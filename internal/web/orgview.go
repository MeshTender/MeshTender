package web

import (
	"context"
	"net/http"
	"strconv"

	"github.com/MeshTender/MeshTender/internal/store"
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

// RegionPalette is the set of translucent fills used to tell regions apart, both as
// the legend's swatches and as the polygon colors on every region map. It lives here
// rather than in regionmap.js so one ordering drives both — a swatch can't drift
// from the shape it labels. Templates pass each region's Color through to the map.
var RegionPalette = []string{
	"#4dabf7", "#f783ac", "#69db7c", "#ffa94d",
	"#9775fa", "#ffd43b", "#3bc9db", "#ff8787",
}

// RegionColor returns the palette entry for the i-th region, wrapping around.
func RegionColor(i int) string { return RegionPalette[i%len(RegionPalette)] }

// RegionView is one region for display, mirroring ProfileView: it carries ID so the
// admin surface can build its edit/area/delete URLs from the same list the
// read-only view renders. HasShape is false for a draft — a region created before
// its area was drawn, which applies nowhere until it has one. Matches reports
// whether the region covers the previewed location (always false without one), which
// drives the highlighting that explains why a token appears in the assembled config.
type RegionView struct {
	ID          int64
	DisplayName string
	Token       string
	Layer       int
	Primary     bool
	AllowFlood  bool
	HasShape    bool
	Matches     bool
	Color       string
	Depth       int
}

// maxRegionDepth caps legend indentation. Layers are arbitrary integers, so a deep
// tree would otherwise indent a row off the side of a narrow column.
const maxRegionDepth = 4

// assignRegionDepths sets each row's Depth to the rank of its layer among the
// distinct layers present, not the layer number itself: an org whose only regions
// are layers 2 and 7 reads as two nested levels rather than seven. Regions arrive
// ordered by (layer, token) from ListRegions, so a single pass suffices.
func assignRegionDepths(rows []RegionView) {
	depth := -1
	prev := 0
	for i := range rows {
		if depth < 0 || rows[i].Layer != prev {
			depth++
			prev = rows[i].Layer
		}
		rows[i].Depth = min(depth, maxRegionDepth)
	}
}

// ConfigView is an org's configuration for display: its named profiles (with one
// selected for its base settings) and its regions, kept independent. Regions are
// listed as the map's legend and drawn on the map itself, so this carries both the
// rows and the geometry. PreviewActive is true when a location was supplied.
//
// The map frames itself from the shapes it draws (and the previewed point), so there
// is no server-computed bounding box.
type ConfigView struct {
	HasConfig       bool
	Profiles        []ProfileView
	Selected        string
	SelectedSteps   []store.ConfigStep
	Regions         []RegionView
	MapRegions      []MapRegion // the shaped regions, as the read-only map draws them
	HasRegions      bool        // org defines at least one region
	HasRegionShapes bool        // some region has a geofence (so the picker map is useful)
	RootAllowFlood  bool        // the org root (*) flood policy — not a region row
	RegionDef       []string    // region def/save commands for the previewed location
	PreviewActive   bool
}

// MapRegion is one region as the read-only config map draws it — the payload handed
// to regionMapView. Only regions with an area appear; a draft has nothing to draw.
// Color comes from RegionPalette by the same index the legend swatch uses, so the
// two always agree.
//
// This ships every region's geometry with the page. That is fine for the handful of
// hand-drawn polygons an org defines; if imported administrative boundaries ever
// make these large, serve them from their own endpoint (or simplify for display)
// rather than growing the HTML.
type MapRegion struct {
	Name    string `json:"name"`
	GeoJSON string `json:"geojson"`
	Matched bool   `json:"matched"`
	Color   string `json:"color"`
	// Primary marks the org's primary region, which the map opens framed on — that
	// is where the org actually operates, so a nationwide parent region shouldn't
	// force a continental view.
	Primary bool `json:"primary"`
}

// ConfigLine is one line of the assembled config preview. FromRegion marks the lines
// contributed by the previewed location's regions rather than by the profile, so the
// page can show which came from where. IsMarker is the {{ region }} placeholder,
// rendered as a hint when no location is picked instead of leaking its literal text.
type ConfigLine struct {
	Text       string
	IsComment  bool
	IsMarker   bool
	FromRegion bool
}

// AssembledLines renders the selected profile and the previewed location's region
// commands as the single ordered list a repeater owner would actually run: the
// profile's steps with the region block spliced in at its {{ region }} marker, or
// appended after every step when it has none.
//
// This is the same assembly the console (console_config.go) and serial setup
// (repeater_setup.go) perform via store.SplitAtRegionMarker — the page used to show
// the two halves as separate blocks and leave the merge to the reader, which meant
// it displayed something subtly different from what those surfaces emit.
//
// With no location picked there are no region lines, and the marker survives as
// IsMarker so the page can say where they will land.
func (cv ConfigView) AssembledLines() []ConfigLine {
	before, after := store.SplitAtRegionMarker(cv.SelectedSteps)
	// No marker means SplitAtRegionMarker put everything in before; the region block
	// then follows all of it, which is the documented default.
	hasMarker := len(after) > 0 || len(before) != len(cv.SelectedSteps)

	out := make([]ConfigLine, 0, len(cv.SelectedSteps)+len(cv.RegionDef))
	emit := func(steps []store.ConfigStep) {
		for _, st := range steps {
			if st.IsComment() {
				out = append(out, ConfigLine{Text: st.Comment, IsComment: true})
				continue
			}
			out = append(out, ConfigLine{Text: st.CommandLine})
		}
	}
	region := func() {
		if len(cv.RegionDef) == 0 {
			// Nothing to splice: keep the marker visible so the page can explain that
			// region commands land here once a location is picked.
			if hasMarker {
				out = append(out, ConfigLine{IsMarker: true})
			}
			return
		}
		for _, line := range cv.RegionDef {
			out = append(out, ConfigLine{Text: line, FromRegion: true})
		}
	}

	emit(before)
	region()
	emit(after)
	return out
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
	for i, z := range regions {
		matched := lat != nil && lon != nil && store.RegionMatches(z, lat, lon)
		if len(z.GeofenceJSON) > 0 {
			cv.MapRegions = append(cv.MapRegions, MapRegion{
				Name: z.DisplayName, GeoJSON: string(z.GeofenceJSON),
				Matched: matched, Color: RegionColor(i), Primary: z.Primary,
			})
		}
		cv.Regions = append(cv.Regions, RegionView{
			ID: z.ID, DisplayName: z.DisplayName, Token: z.Token, Layer: z.Layer,
			Primary: z.Primary, AllowFlood: z.AllowFlood, HasShape: len(z.GeofenceJSON) > 0,
			// RegionMatches is the same predicate RegionDefCommands uses to decide
			// which tokens ship, so the highlight can't disagree with the commands.
			Matches: matched,
			Color:   RegionColor(i),
		})
		if len(z.GeofenceJSON) > 0 {
			cv.HasRegionShapes = true
		}
	}
	assignRegionDepths(cv.Regions)
	// The root (*) flood policy isn't a region row, so it's fetched separately — one
	// PK lookup on the org, needed both by the admin's root switch and by the region
	// def preview.
	cv.RootAllowFlood, err = st.RootAllowFlood(ctx, orgID)
	if err != nil {
		return ConfigView{}, err
	}
	if cv.PreviewActive {
		cv.RegionDef = store.RegionDefCommands(regions, cv.RootAllowFlood, lat, lon)
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
	// even when a member is previewing. IsAdmin keeps the Configuration tab visible
	// on an org that hasn't defined any config yet, so an admin can go and create it.
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
