package core

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/MeshTender/MeshTender/internal/geo"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// Regions are edited one at a time, split by what each part needs from the screen:
//
//   - Attributes (name, token, level, primary, flood) are a small form, edited in a
//     modal on the Configuration page — the same pattern as profiles.
//   - The area is drawn on a map, which needs the width and the spatial context of
//     the neighboring regions, so it gets its own workspace page.
//
// The two save through separate endpoints, so a rejected attribute edit can never
// discard a drawn polygon (and vice versa).

// configRegionView is a region in the admin editor: its display name, MeshCore
// token, layer (depth / region def order), flags, and the raw GeoJSON geofence the
// map editor reads and writes (empty = a draft that applies nowhere yet).
type configRegionView struct {
	ID           int64
	DisplayName  string
	Token        string
	Layer        int
	Primary      bool
	AllowFlood   bool
	GeofenceJSON string
}

// regionAreaSibling is one *other* region of the org, drawn as read-only context on
// the area map so an admin can see how the shape they're editing sits against its
// neighbors.
type regionAreaSibling struct {
	Name    string `json:"name"`
	GeoJSON string `json:"geojson"`
}

// pageRegionEdit renders the region attribute editor: blank for the /new route, or
// pre-filled when a {rid} is present. 404s if the region isn't this org's.
func (s *Handlers) pageRegionEdit(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	// A new region defaults to allowing flood, matching the column default.
	zv := configRegionView{AllowFlood: true}
	if raw := chi.URLParam(r, "rid"); raw != "" {
		rid, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			s.NotFound(w, r)
			return
		}
		z, err := s.Store.GetRegion(r.Context(), orgID, rid)
		if errors.Is(err, store.ErrNotFound) {
			s.NotFound(w, r)
			return
		}
		if err != nil {
			s.ServerError(w, r, "could not load region", err)
			return
		}
		zv = regionView(*z)
	}
	s.renderRegionEdit(w, r, org, zv, nil)
}

// renderRegionEdit renders the attribute editor (shared by the initial GET and the
// error re-render). A zero ID means a new region. An htmx request gets the modal
// fragment the Configuration page swaps in place; anything else (no JS, or a direct
// link) gets the standalone page.
func (s *Handlers) renderRegionEdit(w http.ResponseWriter, r *http.Request, org *store.Org, zv configRegionView, errs []string) {
	data := map[string]any{
		"Org":    org,
		"Nav":    s.orgAdminNav(r, org),
		"Region": zv,
		"Action": regionFormAction(org.Slug, zv.ID),
		"Errors": errs,
	}
	if r.Header.Get("HX-Request") != "" {
		data["Layout"] = "config-region-modal"
	}
	s.Render(w, r, "config_region_edit.html", data)
}

// regionFormAction is where the attribute editor posts: the collection for a new
// region, the region's own URL for an update.
func regionFormAction(slug string, rid int64) string {
	if rid == 0 {
		return "/orgs/" + slug + "/config/regions"
	}
	return "/orgs/" + slug + "/config/regions/" + strconv.FormatInt(rid, 10)
}

// handleCreateRegion validates and inserts a new region.
func (s *Handlers) handleCreateRegion(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	s.saveRegion(w, r, orgID, 0)
}

// handleUpdateRegion validates and updates an existing region's attributes.
func (s *Handlers) handleUpdateRegion(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	rid, ok := s.regionID(r)
	if !ok {
		s.NotFound(w, r)
		return
	}
	s.saveRegion(w, r, orgID, rid)
}

// saveRegion parses the attribute form and creates (rid 0) or updates a region. On
// a validation error it re-renders the editor with the entered values preserved; a
// duplicate token is reported the same way rather than as a server error.
func (s *Handlers) saveRegion(w http.ResponseWriter, r *http.Request, orgID, rid int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	layer, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("region_layer")))
	zv := configRegionView{
		ID:          rid,
		DisplayName: strings.TrimSpace(r.FormValue("region_display")),
		Token:       strings.TrimSpace(r.FormValue("region_token")),
		Layer:       layer,
		Primary:     r.FormValue("region_primary") == "1",
		AllowFlood:  r.FormValue("region_allow_flood") == "1",
	}

	var errs []string
	switch {
	case zv.Token == "":
		errs = append(errs, "Give the region a short name — it's the name MeshCore uses.")
	case !validRegionToken(zv.Token):
		errs = append(errs, fmt.Sprintf("Region name %q may only contain letters, digits, hyphens, or underscores.", zv.Token))
	}
	// The display name is optional; fall back to the token so a row always has a
	// label to show.
	display := zv.DisplayName
	if display == "" {
		display = zv.Token
	}

	if len(errs) == 0 {
		in := store.RegionInput{
			Token: zv.Token, DisplayName: display, Layer: zv.Layer,
			Primary: zv.Primary, AllowFlood: zv.AllowFlood,
		}
		// Only the area endpoint writes a geofence, so neither branch here can drop a
		// shape that's already been drawn.
		if rid == 0 {
			_, err = s.Store.CreateRegion(r.Context(), orgID, in)
		} else {
			err = s.Store.UpdateRegion(r.Context(), orgID, rid, in)
		}
		switch {
		case errors.Is(err, store.ErrDuplicate):
			errs = append(errs, fmt.Sprintf("A region named %q already exists.", zv.Token))
		case errors.Is(err, store.ErrNotFound):
			s.NotFound(w, r)
			return
		case err != nil:
			s.ServerError(w, r, "could not save region", err)
			return
		default:
			// hxRedirect closes the modal and reloads the config page.
			s.hxRedirect(w, r, "/orgs/"+orgParam(r)+"/config")
			return
		}
	}
	s.renderRegionEdit(w, r, org, zv, errs)
}

// handleDeleteRegion removes a region and returns to the config page.
func (s *Handlers) handleDeleteRegion(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	rid, ok := s.regionID(r)
	if !ok {
		s.NotFound(w, r)
		return
	}
	if err := s.Store.DeleteRegion(r.Context(), orgID, rid); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.ServerError(w, r, "could not delete region", err)
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r)+"/config", http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// pageRegionArea renders the area workspace for one region: a full-width map with
// this region's shape editable and its siblings drawn as read-only context.
func (s *Handlers) pageRegionArea(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	rid, ok := s.regionID(r)
	if !ok {
		s.NotFound(w, r)
		return
	}
	regions, err := s.Store.ListRegions(r.Context(), orgID)
	if err != nil {
		s.ServerError(w, r, "could not load regions", err)
		return
	}
	zv, siblings, found := splitRegionAndSiblings(regions, rid)
	if !found {
		s.NotFound(w, r)
		return
	}
	s.renderRegionArea(w, r, org, zv, siblings, nil)
}

// renderRegionArea renders the area workspace (shared by the initial GET and the
// error re-render, which keeps the submitted shape so a bad polygon isn't lost).
func (s *Handlers) renderRegionArea(w http.ResponseWriter, r *http.Request, org *store.Org, zv configRegionView, siblings []regionAreaSibling, errs []string) {
	s.Render(w, r, "config_region_area.html", map[string]any{
		"Org":      org,
		"Nav":      s.orgAdminNav(r, org),
		"Region":   zv,
		"Siblings": siblings,
		"Errors":   errs,
	})
}

// handleSaveRegionArea saves just this region's drawn area. An empty shape clears it
// back to a draft (the "Clear area" path), which is allowed — a region with no area
// simply applies nowhere until one is drawn.
func (s *Handlers) handleSaveRegionArea(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	rid, ok := s.regionID(r)
	if !ok {
		s.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	gj := strings.TrimSpace(r.FormValue("region_geojson"))

	var errs []string
	geofence, valid := regionGeofence(gj, &errs)
	if valid {
		err := s.Store.UpdateRegionGeofence(r.Context(), orgID, rid, geofence)
		switch {
		case errors.Is(err, store.ErrNotFound):
			s.NotFound(w, r)
			return
		case err != nil:
			s.ServerError(w, r, "could not save area", err)
			return
		}
		http.Redirect(w, r, "/orgs/"+orgParam(r)+"/config", http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
		return
	}
	// Re-render with the rejected shape still in hand so the admin can fix it.
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	regions, err := s.Store.ListRegions(r.Context(), orgID)
	if err != nil {
		s.ServerError(w, r, "could not load regions", err)
		return
	}
	zv, siblings, found := splitRegionAndSiblings(regions, rid)
	if !found {
		s.NotFound(w, r)
		return
	}
	zv.GeofenceJSON = gj
	s.renderRegionArea(w, r, org, zv, siblings, errs)
}

// handleSetRootFlood toggles the org's root (*) flood policy. The root isn't a
// region row, so it saves on its own from the Configuration page.
func (s *Handlers) handleSetRootFlood(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if err := s.Store.SetRootAllowFlood(r.Context(), orgID, r.FormValue("root_allow_flood") == "1"); err != nil {
		s.ServerError(w, r, "could not save flood policy", err)
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r)+"/config", http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// regionID reads the {rid} URL param.
func (s *Handlers) regionID(r *http.Request) (int64, bool) {
	rid, err := strconv.ParseInt(chi.URLParam(r, "rid"), 10, 64)
	return rid, err == nil
}

// orgAdminNav builds the org sub-navigation for the admin config screens, which are
// all admin-only members-area pages on the Configuration tab.
func (s *Handlers) orgAdminNav(r *http.Request, org *store.Org) map[string]any {
	return s.OrgNavFor(r.Context(), web.OrgNavArgs{
		OrgID: org.ID, Name: org.Name, Slug: org.Slug, Active: "config",
		IsMember: true, IsAdmin: true, Manage: true,
	})
}

// splitRegionAndSiblings picks the target region out of an org's regions and turns
// the rest into read-only map context. Only siblings with a drawn shape are
// included — a draft has nothing to render.
func splitRegionAndSiblings(regions []store.Region, rid int64) (configRegionView, []regionAreaSibling, bool) {
	var zv configRegionView
	found := false
	var siblings []regionAreaSibling
	for _, z := range regions {
		if z.ID == rid {
			zv, found = regionView(z), true
			continue
		}
		if len(z.GeofenceJSON) > 0 {
			siblings = append(siblings, regionAreaSibling{Name: z.DisplayName, GeoJSON: string(z.GeofenceJSON)})
		}
	}
	return zv, siblings, found
}

// regionView converts a stored region into an editor view, carrying its geofence's
// raw GeoJSON through verbatim so the map editor round-trips arbitrary polygons.
func regionView(z store.Region) configRegionView {
	return configRegionView{
		ID: z.ID, DisplayName: z.DisplayName, Token: z.Token, Layer: z.Layer,
		Primary: z.Primary, AllowFlood: z.AllowFlood, GeofenceJSON: string(z.GeofenceJSON),
	}
}

// validRegionToken reports whether s is a usable MeshCore region token: non-empty
// and limited to letters, digits, hyphens, and underscores. Tokens are space-joined
// into a single `region def` line, so spaces and the |/, separators aren't allowed.
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

// regionGeofence validates the GeoJSON drawn for a region. An empty shape is a
// draft (nil geofence, applies nowhere); anything else must parse as a GeoJSON
// Polygon/MultiPolygon.
func regionGeofence(gj string, errs *[]string) ([]byte, bool) {
	gj = strings.TrimSpace(gj)
	if gj == "" {
		return nil, true // draft — no area yet
	}
	if _, err := geo.Parse([]byte(gj)); err != nil {
		*errs = append(*errs, "That map shape isn't valid — redraw the area and try again.")
		return nil, false
	}
	return []byte(gj), true
}
