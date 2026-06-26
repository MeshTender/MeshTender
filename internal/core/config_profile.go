package core

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jleight/meshtender/internal/geo"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// Bounds on how many profiles/regions one save may define, so a malformed or
// abusive submission can't insert unbounded rows.
const (
	maxConfigProfiles = 50
	maxConfigRegions  = 50
)

// configProfileView is a named base-settings profile in the admin editor.
type configProfileView struct {
	Name      string
	StepsText string             // editor: one command per line
	Steps     []store.ConfigStep // read-only: rendered list
}

// configRegionView is a region in the admin editor: its name/priority, the raw
// GeoJSON geofence the map editor reads and writes (empty = applies everywhere),
// and its steps (one command per line).
type configRegionView struct {
	Name         string
	Priority     int
	GeofenceJSON string
	StepsText    string
	Steps        []store.ConfigStep
}

// pageOrgConfig renders an org's configuration read-only: a selected profile's
// base settings plus the regions (location steps). Visible to any signed-in user;
// admins get an Edit button. ?profile= chooses which profile to show; ?lat=&lon=
// marks which regions apply at a location. The root host serves it anonymously.
func (s *Handlers) pageOrgConfig(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	org, err := s.Store.GetOrg(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	role, isMember, err := s.Store.OrgRole(r.Context(), id, uid)
	if err != nil {
		http.Error(w, "could not load org", http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"Org":     org,
		"Nav":     s.OrgNavFor(r.Context(), id, org.Slug, "config", isMember, role == "admin"),
		"CanEdit": role == "admin",
	}
	var latP, lonP *float64
	if lat, lon, ok := web.PreviewLatLon(r); ok {
		latP, lonP = &lat, &lon
		data["PreviewLat"], data["PreviewLon"] = lat, lon
	}
	cv, err := web.BuildConfigView(r.Context(), s.Store, id, r.URL.Query().Get("profile"), latP, lonP)
	if err != nil {
		http.Error(w, "could not load config", http.StatusInternalServerError)
		return
	}
	data["Config"] = cv
	s.Render(w, r, "org_config.html", data)
}

// pageOrgConfigEdit is the admin editor for the org's profiles and regions.
func (s *Handlers) pageOrgConfigEdit(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	profiles, err := s.Store.ListProfiles(r.Context(), orgID)
	if err != nil {
		http.Error(w, "could not load config", http.StatusInternalServerError)
		return
	}
	regions, err := s.Store.ListRegions(r.Context(), orgID)
	if err != nil {
		http.Error(w, "could not load config", http.StatusInternalServerError)
		return
	}
	s.Render(w, r, "config_edit.html", map[string]any{
		"Org":          org,
		"Nav":          s.OrgNavFor(r.Context(), orgID, org.Slug, "config", true, true),
		"EmptyProfile": configProfileView{},
		"EmptyRegion":  configRegionView{},
		"Profiles":     profileViews(profiles),
		"Regions":      regionViews(regions),
	})
}

// handleSaveOrgConfig validates and replaces the org's profiles + regions (admin
// only). Any unknown command line blocks the save and the editor is re-rendered
// with the errors and the entered text preserved.
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
	profiles, profileVs := s.parseProfiles(r, catalog, &errs, &risky)
	regions, regionVs := s.parseRegions(r, catalog, &errs, &risky)

	if len(errs) > 0 {
		org, gerr := s.Store.GetOrg(r.Context(), orgID)
		if gerr != nil {
			http.NotFound(w, r)
			return
		}
		s.Render(w, r, "config_edit.html", map[string]any{
			"Org":          org,
			"Nav":          s.OrgNavFor(r.Context(), orgID, org.Slug, "config", true, true),
			"EmptyProfile": configProfileView{},
			"EmptyRegion":  configRegionView{},
			"Errors":       errs,
			"RiskyWarn":    risky,
			"Profiles":     profileVs,
			"Regions":      regionVs,
		})
		return
	}

	if err := s.Store.ReplaceOrgConfig(r.Context(), orgID, profiles, regions); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r)+"/config", http.StatusSeeOther)
}

// parseProfiles reads the repeated profile blocks from the form into store inputs
// and editor views (for re-display on error). Empty blocks are ignored; a block
// with steps but no name, or a duplicate name, is an error.
func (s *Handlers) parseProfiles(r *http.Request, catalog []*store.Command, errs, risky *[]string) ([]store.ProfileInput, []configProfileView) {
	names := r.Form["profile_name"]
	if len(names) > maxConfigProfiles {
		*errs = append(*errs, fmt.Sprintf("Too many profiles (max %d).", maxConfigProfiles))
	}
	seen := map[string]bool{}
	var ins []store.ProfileInput
	var views []configProfileView
	for i := range names {
		name := strings.TrimSpace(formAt(r, "profile_name", i))
		stepsText := formAt(r, "profile_steps", i)
		if name == "" && strings.TrimSpace(stepsText) == "" {
			continue // empty block
		}
		view := configProfileView{Name: name, StepsText: stepsText}
		if name == "" {
			*errs = append(*errs, "A profile is missing a name.")
			views = append(views, view)
			continue
		}
		if seen[strings.ToLower(name)] {
			*errs = append(*errs, fmt.Sprintf("Duplicate profile name %q.", name))
			views = append(views, view)
			continue
		}
		seen[strings.ToLower(name)] = true
		steps, storeSteps := parseConfigSteps(stepsText, catalog, fmt.Sprintf("profile %q", name), errs, risky)
		view.Steps = steps
		views = append(views, view)
		ins = append(ins, store.ProfileInput{Name: name, Steps: storeSteps})
	}
	return ins, views
}

// parseRegions reads the repeated region blocks from the form.
func (s *Handlers) parseRegions(r *http.Request, catalog []*store.Command, errs, risky *[]string) ([]store.RegionInput, []configRegionView) {
	names := r.Form["region_name"]
	if len(names) > maxConfigRegions {
		*errs = append(*errs, fmt.Sprintf("Too many regions (max %d).", maxConfigRegions))
	}
	seen := map[string]bool{}
	var ins []store.RegionInput
	var views []configRegionView
	for i := range names {
		name := strings.TrimSpace(formAt(r, "region_name", i))
		stepsText := formAt(r, "region_steps", i)
		zv := configRegionView{
			Name: name, StepsText: stepsText,
			GeofenceJSON: strings.TrimSpace(formAt(r, "region_geojson", i)),
		}
		if name == "" && strings.TrimSpace(stepsText) == "" {
			continue
		}
		if name == "" {
			*errs = append(*errs, "A region is missing a name.")
			views = append(views, zv)
			continue
		}
		if seen[strings.ToLower(name)] {
			*errs = append(*errs, fmt.Sprintf("Duplicate region name %q.", name))
			views = append(views, zv)
			continue
		}
		seen[strings.ToLower(name)] = true
		priority, _ := strconv.Atoi(formAt(r, "region_priority", i))
		zv.Priority = priority
		steps, storeSteps := parseConfigSteps(stepsText, catalog, fmt.Sprintf("region %q", name), errs, risky)
		zv.Steps = steps
		geofence, ok := regionGeofence(zv, name, errs)
		if !ok {
			views = append(views, zv)
			continue
		}
		views = append(views, zv)
		ins = append(ins, store.RegionInput{Name: name, Priority: priority, GeofenceJSON: geofence, Steps: storeSteps})
	}
	return ins, views
}

// formAt returns the i-th value of a repeated form field, or "".
func formAt(r *http.Request, field string, i int) string {
	v := r.Form[field]
	if i < len(v) {
		return v[i]
	}
	return ""
}

// regionGeofence validates the GeoJSON drawn for a region. An empty shape is a
// region that applies everywhere (nil geofence); anything else must parse as a
// GeoJSON Polygon/MultiPolygon.
func regionGeofence(zv configRegionView, name string, errs *[]string) ([]byte, bool) {
	gj := strings.TrimSpace(zv.GeofenceJSON)
	if gj == "" {
		return nil, true // everywhere
	}
	if _, err := geo.Parse([]byte(gj)); err != nil {
		*errs = append(*errs, fmt.Sprintf("Region %q has an invalid map shape — redraw it on the map.", name))
		return nil, false
	}
	return []byte(gj), true
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

// profileViews converts stored profiles into editor/read views.
func profileViews(profiles []store.Profile) []configProfileView {
	out := make([]configProfileView, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, configProfileView{Name: p.Name, StepsText: stepsToText(p.Steps), Steps: p.Steps})
	}
	return out
}

// regionViews converts stored regions into editor views, carrying each geofence's
// raw GeoJSON through verbatim so the map editor round-trips arbitrary polygons.
func regionViews(regions []store.Region) []configRegionView {
	out := make([]configRegionView, 0, len(regions))
	for _, z := range regions {
		out = append(out, configRegionView{
			Name: z.Name, Priority: z.Priority,
			GeofenceJSON: string(z.GeofenceJSON),
			StepsText:    stepsToText(z.Steps), Steps: z.Steps,
		})
	}
	return out
}

// repeaterProfiles is an org's region steps resolved for a specific repeater's
// location, for the console reference block. Base-settings profiles are a viewing
// choice on the org config page, so they aren't auto-applied here.
type repeaterProfiles struct {
	OrgName string
	OrgSlug string
	Steps   []store.ConfigStep
}

// resolvedProfilesForRepeater returns, for each org the repeater is contributed to
// that has regions, the region steps resolved for the repeater's location.
func (s *Handlers) resolvedProfilesForRepeater(r *http.Request, rep *store.Repeater) []repeaterProfiles {
	orgs, err := s.Store.ListRepeaterOrgs(r.Context(), rep.ID)
	if err != nil {
		return nil
	}
	var out []repeaterProfiles
	for _, o := range orgs {
		regions, err := s.Store.ListRegions(r.Context(), o.OrgID)
		if err != nil {
			continue
		}
		steps := store.ResolveRegions(regions, rep.Latitude, rep.Longitude)
		if len(steps) == 0 {
			continue
		}
		out = append(out, repeaterProfiles{OrgName: o.OrgName, OrgSlug: o.OrgSlug, Steps: steps})
	}
	return out
}
