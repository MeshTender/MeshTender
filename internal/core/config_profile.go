package core

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/geo"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// Bound on how many regions one save may define, so a malformed or abusive
// submission can't insert unbounded rows. (Profiles are added one at a time.)
const maxConfigRegions = 50

// configRegionView is a region in the admin editor: its display name, MeshCore
// token, layer (depth / region def order), and the raw GeoJSON geofence the map
// editor reads and writes (empty = applies everywhere).
type configRegionView struct {
	DisplayName  string
	Token        string
	Layer        int
	Primary      bool
	AllowFlood   bool
	GeofenceJSON string
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

// pageConfigHub is the admin configuration overview: a list of profiles (each
// edited on its own page) and a summary of the org's regions (edited on the map
// page). It replaces the old single mega-form.
func (s *Handlers) pageConfigHub(w http.ResponseWriter, r *http.Request) {
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
	var primary string
	for _, z := range regions {
		if z.Primary {
			primary = z.DisplayName
			break
		}
	}
	s.Render(w, r, "config_edit.html", map[string]any{
		"Org":           org,
		"Nav":           s.OrgNavFor(r.Context(), orgID, org.Slug, "config", true, true),
		"Profiles":      profiles,
		"RegionCount":   len(regions),
		"PrimaryRegion": primary,
	})
}

// pageProfileEdit renders the single-profile editor: blank for the /new route, or
// pre-filled when a {pid} is present. 404s if the profile isn't this org's.
func (s *Handlers) pageProfileEdit(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var pid int64
	var name, stepsText string
	if raw := chi.URLParam(r, "pid"); raw != "" {
		pid, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		p, err := s.Store.GetProfile(r.Context(), orgID, pid)
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "could not load profile", http.StatusInternalServerError)
			return
		}
		name, stepsText = p.Name, stepsToText(p.Steps)
	}
	s.renderProfileEdit(w, r, org, pid, name, stepsText, nil, nil)
}

// renderProfileEdit renders the profile editor page (shared by the initial GET
// and the error re-render). pid 0 means a new profile.
func (s *Handlers) renderProfileEdit(w http.ResponseWriter, r *http.Request, org *store.Org, pid int64, name, stepsText string, errs, risky []string) {
	s.Render(w, r, "config_profile_edit.html", map[string]any{
		"Org":       org,
		"Nav":       s.OrgNavFor(r.Context(), org.ID, org.Slug, "config", true, true),
		"ProfileID": pid,
		"Name":      name,
		"StepsText": stepsText,
		"Errors":    errs,
		"RiskyWarn": risky,
	})
}

// handleCreateProfile validates and inserts a new profile.
func (s *Handlers) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	s.saveProfile(w, r, orgID, 0)
}

// handleUpdateProfile validates and updates an existing profile.
func (s *Handlers) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	pid, err := strconv.ParseInt(chi.URLParam(r, "pid"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.saveProfile(w, r, orgID, pid)
}

// saveProfile parses the single-profile form and creates (pid 0) or updates it.
// On a validation error it re-renders the editor with the entered text preserved;
// on a duplicate name it reports a friendly message the same way.
func (s *Handlers) saveProfile(w http.ResponseWriter, r *http.Request, orgID, pid int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	catalog, err := s.Store.ListCommands(r.Context())
	if err != nil {
		http.Error(w, "could not load commands", http.StatusInternalServerError)
		return
	}
	name := strings.TrimSpace(r.FormValue("profile_name"))
	stepsText := r.FormValue("profile_steps")
	var errs, risky []string
	if name == "" {
		errs = append(errs, "Give the profile a name.")
	}
	_, steps := parseConfigSteps(stepsText, catalog, "profile", &errs, &risky)

	if len(errs) == 0 {
		if pid == 0 {
			_, err = s.Store.CreateProfile(r.Context(), orgID, name, steps)
		} else {
			err = s.Store.UpdateProfile(r.Context(), orgID, pid, name, steps)
		}
		switch {
		case errors.Is(err, store.ErrDuplicate):
			errs = append(errs, fmt.Sprintf("A profile named %q already exists.", name))
		case errors.Is(err, store.ErrNotFound):
			http.NotFound(w, r)
			return
		case err != nil:
			http.Error(w, "could not save profile", http.StatusInternalServerError)
			return
		default:
			http.Redirect(w, r, "/orgs/"+orgParam(r)+"/config/edit", http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
			return
		}
	}
	s.renderProfileEdit(w, r, org, pid, name, stepsText, errs, risky)
}

// handleDeleteProfile removes a profile and returns to the config hub.
func (s *Handlers) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	pid, err := strconv.ParseInt(chi.URLParam(r, "pid"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.Store.DeleteProfile(r.Context(), orgID, pid); err != nil && !errors.Is(err, store.ErrNotFound) {
		http.Error(w, "could not delete profile", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r)+"/config/edit", http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// pageRegionsEdit renders the region map editor (map + side list).
func (s *Handlers) pageRegionsEdit(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	regions, err := s.Store.ListRegions(r.Context(), orgID)
	if err != nil {
		http.Error(w, "could not load regions", http.StatusInternalServerError)
		return
	}
	s.Render(w, r, "config_regions_edit.html", map[string]any{
		"Org":            org,
		"Nav":            s.OrgNavFor(r.Context(), orgID, org.Slug, "config", true, true),
		"EmptyRegion":    configRegionView{AllowFlood: true},
		"Regions":        regionViews(regions),
		"RootAllowFlood": org.RootAllowFlood,
	})
}

// handleSaveRegions validates and atomically replaces just the org's regions
// (profiles are left untouched). On error the editor is re-rendered with input
// preserved.
func (s *Handlers) handleSaveRegions(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	rootAllowFlood := r.FormValue("root_allow_flood") == "1"
	var errs []string
	regions, regionVs := s.parseRegions(r, &errs)
	if len(errs) > 0 {
		org, gerr := s.Store.GetOrg(r.Context(), orgID)
		if gerr != nil {
			http.NotFound(w, r)
			return
		}
		s.Render(w, r, "config_regions_edit.html", map[string]any{
			"Org":            org,
			"Nav":            s.OrgNavFor(r.Context(), orgID, org.Slug, "config", true, true),
			"EmptyRegion":    configRegionView{AllowFlood: true},
			"Errors":         errs,
			"Regions":        regionVs,
			"RootAllowFlood": rootAllowFlood,
		})
		return
	}
	if err := s.Store.ReplaceRegions(r.Context(), orgID, regions, rootAllowFlood); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r)+"/config", http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// parseRegions reads the repeated region blocks from the form into store inputs
// and editor views. A region needs a token (its MeshCore name); the display name
// defaults to the token when left blank. Tokens must be unique and use only
// letters, digits, hyphens, or underscores (they are space-joined into a single
// region def line, so spaces and the |/, separators are not allowed).
func (s *Handlers) parseRegions(r *http.Request, errs *[]string) ([]store.RegionInput, []configRegionView) {
	tokens := r.Form["region_token"]
	if len(tokens) > maxConfigRegions {
		*errs = append(*errs, fmt.Sprintf("Too many regions (max %d).", maxConfigRegions))
	}
	seen := map[string]bool{}
	primaryTaken := false
	var ins []store.RegionInput
	var views []configRegionView
	for i := range tokens {
		token := strings.TrimSpace(formAt(r, "region_token", i))
		display := strings.TrimSpace(formAt(r, "region_display", i))
		geojson := strings.TrimSpace(formAt(r, "region_geojson", i))
		layer, _ := strconv.Atoi(formAt(r, "region_layer", i))
		primary := formAt(r, "region_primary", i) == "1"
		allowFlood := formAt(r, "region_allow_flood", i) == "1"
		if token == "" && display == "" && geojson == "" {
			continue // empty block
		}
		zv := configRegionView{DisplayName: display, Token: token, Layer: layer, Primary: primary, AllowFlood: allowFlood, GeofenceJSON: geojson}
		if token == "" {
			*errs = append(*errs, "A region is missing its short name.")
			views = append(views, zv)
			continue
		}
		if !validRegionToken(token) {
			*errs = append(*errs, fmt.Sprintf("Region name %q may only contain letters, digits, hyphens, or underscores.", token))
			views = append(views, zv)
			continue
		}
		if seen[strings.ToLower(token)] {
			*errs = append(*errs, fmt.Sprintf("Duplicate region name %q.", token))
			views = append(views, zv)
			continue
		}
		seen[strings.ToLower(token)] = true
		if display == "" {
			display = token
			zv.DisplayName = display
		}
		// Non-root regions must have a drawn area; the root region (*) is the only
		// "applies everywhere" region and lives on the org, not in this list.
		if geojson == "" {
			*errs = append(*errs, fmt.Sprintf("Region %q needs a drawn area — outline it on the map, or rely on the root region.", token))
			views = append(views, zv)
			continue
		}
		geofence, ok := regionGeofence(zv, token, errs)
		if !ok {
			views = append(views, zv)
			continue
		}
		// Only one region may be primary; keep the first and clear any later ones.
		if primary && primaryTaken {
			primary, zv.Primary = false, false
		} else if primary {
			primaryTaken = true
		}
		views = append(views, zv)
		ins = append(ins, store.RegionInput{Token: token, DisplayName: display, Layer: layer, Primary: primary, AllowFlood: allowFlood, GeofenceJSON: geofence})
	}
	return ins, views
}

// validRegionToken reports whether s is a usable MeshCore region token: non-empty
// and limited to letters, digits, hyphens, and underscores.
func validRegionToken(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
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

// regionViews converts stored regions into editor views, carrying each geofence's
// raw GeoJSON through verbatim so the map editor round-trips arbitrary polygons.
func regionViews(regions []store.Region) []configRegionView {
	out := make([]configRegionView, 0, len(regions))
	for _, z := range regions {
		out = append(out, configRegionView{
			DisplayName: z.DisplayName, Token: z.Token, Layer: z.Layer, Primary: z.Primary,
			AllowFlood: z.AllowFlood, GeofenceJSON: string(z.GeofenceJSON),
		})
	}
	return out
}

// repeaterProfiles is an org's region-def commands resolved for a specific
// repeater's location, for the console reference block. Base-settings profiles are
// a viewing choice on the org config page, so they aren't auto-applied here.
type repeaterProfiles struct {
	OrgName  string
	OrgSlug  string
	Commands []string
}

// resolvedProfilesForRepeater returns, for each org the repeater is contributed to
// whose regions cover the repeater's location, the `region def`/`region save`
// commands to apply that region hierarchy.
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
		rootAllow, err := s.Store.RootAllowFlood(r.Context(), o.OrgID)
		if err != nil {
			continue
		}
		cmds := store.RegionDefCommands(regions, rootAllow, rep.Latitude, rep.Longitude)
		if len(cmds) == 0 {
			continue
		}
		out = append(out, repeaterProfiles{OrgName: o.OrgName, OrgSlug: o.OrgSlug, Commands: cmds})
	}
	return out
}
