package core

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jleight/meshtender/internal/geo"
	"github.com/jleight/meshtender/internal/store"
)

// maxConfigZones bounds how many zones one profile version may define, so a
// malformed or abusive submission can't insert unbounded rows.
const maxConfigZones = 50

// requireOrgMember resolves {id} and verifies the current user belongs to the org
// (admin or member). Returns the org id, the role, and ok.
func (s *Handlers) requireOrgMember(w http.ResponseWriter, r *http.Request) (int64, string, bool) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		http.NotFound(w, r)
		return 0, "", false
	}
	role, isMember, err := s.Store.OrgRole(r.Context(), id, uid)
	if err != nil || !isMember {
		http.NotFound(w, r)
		return 0, "", false
	}
	return id, role, true
}

// configZoneView is a zone rendered on the config page: the read-only block shows
// its steps; the admin editor reuses the same fields (rectangle corners derived
// from the stored geofence's bounds) to repopulate the form.
type configZoneView struct {
	Name      string
	Priority  int
	MatchAll  bool
	MinLat    string
	MinLon    string
	MaxLat    string
	MaxLon    string
	StepsText string            // editor: one command per line
	Steps     []store.ConfigStep // read-only: rendered list
}

// pageOrgConfig renders an org's recommended configuration. Any member sees the
// read-only reference; admins additionally get the editor. An optional ?lat=&lon=
// previews the resolved command list for a sample location.
func (s *Handlers) pageOrgConfig(w http.ResponseWriter, r *http.Request) {
	orgID, role, ok := s.requireOrgMember(w, r)
	if !ok {
		return
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	data := map[string]any{
		"Org":       org,
		"IsAdmin":   role == "admin",
		"EmptyZone": configZoneView{}, // prototype for the "add zone" cloner
	}

	vid, version, err := s.Store.CurrentProfileVersion(r.Context(), orgID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// No profile yet — the editor starts empty; the read view shows an empty state.
	case err != nil:
		http.Error(w, "could not load profile", http.StatusInternalServerError)
		return
	default:
		base, zones, perr := s.Store.ProfileVersion(r.Context(), vid)
		if perr != nil {
			http.Error(w, "could not load profile", http.StatusInternalServerError)
			return
		}
		data["HasProfile"] = true
		data["Version"] = version
		data["BaseSteps"] = base
		data["BaseText"] = stepsToText(base)
		data["Zones"] = zoneViews(zones)

		if lat, lon, ok := previewLatLon(r); ok {
			data["Preview"] = store.ResolveProfile(base, zones, &lat, &lon)
			data["PreviewLat"], data["PreviewLon"] = lat, lon
		}
	}

	s.Render(w, r, "config.html", data)
}

// handleSaveOrgConfig validates and publishes a new profile version (admin only).
// Any unknown command line blocks the publish and the editor is re-rendered with
// the errors and the entered text preserved.
func (s *Handlers) handleSaveOrgConfig(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	catalog, err := s.Store.ListCommands(r.Context())
	if err != nil {
		http.Error(w, "could not load commands", http.StatusInternalServerError)
		return
	}

	var errs, risky []string
	_, baseSteps := parseConfigSteps(r.FormValue("base"), catalog, "base steps", &errs, &risky)

	names := r.Form["zone_name"]
	if len(names) > maxConfigZones {
		errs = append(errs, fmt.Sprintf("Too many zones (max %d).", maxConfigZones))
	}
	var zones []store.ZoneInput
	var zoneViewsForRedisplay []configZoneView
	for i := range names {
		zv, zi, ok := s.parseZone(r, i, catalog, &errs, &risky)
		if !ok {
			continue
		}
		zones = append(zones, zi)
		zoneViewsForRedisplay = append(zoneViewsForRedisplay, zv)
	}

	if len(errs) > 0 {
		org, gerr := s.Store.GetOrg(r.Context(), orgID)
		if gerr != nil {
			http.NotFound(w, r)
			return
		}
		s.Render(w, r, "config.html", map[string]any{
			"Org":       org,
			"IsAdmin":   true,
			"EmptyZone": configZoneView{},
			"Errors":    errs,
			"RiskyWarn": risky,
			"BaseText":  r.FormValue("base"),
			"Zones":     zoneViewsForRedisplay,
			// Keep the editor open with the submitted content even on the first save.
			"HasProfile": true,
		})
		return
	}

	uid := s.Auth.CurrentUserID(r.Context())
	note := strings.TrimSpace(r.FormValue("note"))
	if _, err := s.Store.PublishProfileVersion(r.Context(), orgID, note, uid, baseSteps, zones); err != nil {
		http.Error(w, "could not publish", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r)+"/config", http.StatusSeeOther)
}

// parseZone reads the i-th zone block from the form into both an editor view (for
// re-display on error) and a store input. ok is false when the zone is unusable
// (e.g. a malformed box) — an error is recorded in that case.
func (s *Handlers) parseZone(r *http.Request, i int, catalog []*store.Command, errs, risky *[]string) (configZoneView, store.ZoneInput, bool) {
	get := func(field string) string {
		v := r.Form[field]
		if i < len(v) {
			return strings.TrimSpace(v[i])
		}
		return ""
	}
	name := get("zone_name")
	stepsText := get("zone_steps")
	zv := configZoneView{
		Name: name, StepsText: stepsText,
		MinLat: get("zone_minlat"), MinLon: get("zone_minlon"),
		MaxLat: get("zone_maxlat"), MaxLon: get("zone_maxlon"),
	}
	if name == "" && strings.TrimSpace(stepsText) == "" {
		return zv, store.ZoneInput{}, false // empty block — ignore
	}
	if name == "" {
		*errs = append(*errs, "A zone is missing a name.")
		return zv, store.ZoneInput{}, false
	}
	priority, _ := strconv.Atoi(get("zone_priority"))
	zv.Priority = priority

	label := fmt.Sprintf("zone %q", name)
	steps, storeSteps := parseConfigSteps(stepsText, catalog, label, errs, risky)
	zv.Steps = steps

	geofence, ok := zoneGeofence(zv, name, errs)
	if !ok {
		return zv, store.ZoneInput{}, false
	}
	return zv, store.ZoneInput{Name: name, Priority: priority, GeofenceJSON: geofence, Steps: storeSteps}, true
}

// zoneGeofence builds the GeoJSON for a zone from its rectangle corners. All four
// blank = a match-all zone (nil geofence). A partial box is an error.
func zoneGeofence(zv configZoneView, name string, errs *[]string) ([]byte, bool) {
	boxes := []string{zv.MinLat, zv.MinLon, zv.MaxLat, zv.MaxLon}
	blank := 0
	for _, b := range boxes {
		if b == "" {
			blank++
		}
	}
	if blank == 4 {
		return nil, true // match-all
	}
	if blank != 0 {
		*errs = append(*errs, fmt.Sprintf("Zone %q needs all four corner coordinates (or leave all blank for everywhere).", name))
		return nil, false
	}
	vals := make([]float64, 4)
	for j, b := range boxes {
		f, err := strconv.ParseFloat(b, 64)
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("Zone %q has an invalid coordinate %q.", name, b))
			return nil, false
		}
		vals[j] = f
	}
	return geo.Rectangle(vals[0], vals[1], vals[2], vals[3]), true
}

// parseConfigSteps turns textarea lines into config steps, validating each command
// against the catalog with the same resolver the console uses. Blank lines are
// skipped; "# ..." lines become comment steps; unknown commands are recorded in
// errs and risky commands in risky. It returns view steps (with rendered fields)
// and the store steps to persist.
func parseConfigSteps(text string, catalog []*store.Command, label string, errs, risky *[]string) (view, persist []store.ConfigStep) {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			comment := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			step := store.ConfigStep{Comment: comment}
			view = append(view, step)
			persist = append(persist, step)
			continue
		}
		if !validCommandText(line) {
			*errs = append(*errs, fmt.Sprintf("%s: invalid command %q.", label, line))
			continue
		}
		cmd := resolveCommand(line, catalog)
		if cmd == nil {
			*errs = append(*errs, fmt.Sprintf("%s: unknown command %q.", label, line))
			continue
		}
		if cmd.Risky {
			*risky = append(*risky, line)
		}
		id := cmd.ID
		step := store.ConfigStep{CommandLine: line, CommandID: &id}
		view = append(view, step)
		persist = append(persist, step)
	}
	return view, persist
}

// stepsToText renders stored steps back into editable textarea content: commands
// as-is, comment steps prefixed with "# ".
func stepsToText(steps []store.ConfigStep) string {
	var b strings.Builder
	for _, s := range steps {
		if s.IsComment() {
			b.WriteString("# ")
			b.WriteString(s.Comment)
		} else {
			b.WriteString(s.CommandLine)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// zoneViews converts stored zones into editor/read views, deriving the rectangle
// corners from each geofence's bounding box.
func zoneViews(zones []store.Zone) []configZoneView {
	out := make([]configZoneView, 0, len(zones))
	for _, z := range zones {
		zv := configZoneView{
			Name: z.Name, Priority: z.Priority,
			StepsText: stepsToText(z.Steps), Steps: z.Steps,
		}
		if minLat, minLon, maxLat, maxLon, ok := z.Geofence.Bounds(); ok {
			zv.MinLat = formatCoord(minLat)
			zv.MinLon = formatCoord(minLon)
			zv.MaxLat = formatCoord(maxLat)
			zv.MaxLon = formatCoord(maxLon)
		} else {
			zv.MatchAll = true
		}
		out = append(out, zv)
	}
	return out
}

func formatCoord(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// previewLatLon parses the optional ?lat=&lon= preview coordinates.
func previewLatLon(r *http.Request) (lat, lon float64, ok bool) {
	ls, ns := r.URL.Query().Get("lat"), r.URL.Query().Get("lon")
	if ls == "" || ns == "" {
		return 0, 0, false
	}
	var err1, err2 error
	lat, err1 = strconv.ParseFloat(ls, 64)
	lon, err2 = strconv.ParseFloat(ns, 64)
	return lat, lon, err1 == nil && err2 == nil
}

// repeaterProfiles is a contributed org's recommended configuration resolved for a
// specific repeater's location, for the console reference block.
type repeaterProfiles struct {
	OrgName string
	OrgSlug string
	Steps   []store.ConfigStep
}

// resolvedProfilesForRepeater returns, for each org the repeater is contributed to
// that has a published profile, the steps resolved for the repeater's location.
func (s *Handlers) resolvedProfilesForRepeater(r *http.Request, rep *store.Repeater) []repeaterProfiles {
	orgs, err := s.Store.ListRepeaterOrgs(r.Context(), rep.ID)
	if err != nil {
		return nil
	}
	var out []repeaterProfiles
	for _, o := range orgs {
		vid, _, err := s.Store.CurrentProfileVersion(r.Context(), o.OrgID)
		if err != nil {
			continue // ErrNotFound (no profile) or transient — just skip
		}
		base, zones, err := s.Store.ProfileVersion(r.Context(), vid)
		if err != nil {
			continue
		}
		steps := store.ResolveProfile(base, zones, rep.Latitude, rep.Longitude)
		if len(steps) == 0 {
			continue
		}
		out = append(out, repeaterProfiles{OrgName: o.OrgName, OrgSlug: o.OrgSlug, Steps: steps})
	}
	return out
}
