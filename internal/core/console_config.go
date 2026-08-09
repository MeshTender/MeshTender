package core

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// consoleConfig is the JSON payload behind the console's "Apply organization
// configuration" modal: the orgs whose config applies to this repeater, the
// selected org/profile, the full recommended-command list (profile base settings
// + region commands), and the location state that drives the region commands.
type consoleConfig struct {
	Orgs            []store.RepeaterConfigOrg `json:"orgs"`
	SelectedOrg     int64                     `json:"selectedOrg"`
	SelectedProfile string                    `json:"selectedProfile"`
	Commands        []consoleConfigCommand    `json:"commands"`
	Location        consoleConfigLocation     `json:"location"`
}

// consoleConfigCommand is one line of the recommended configuration. Every line
// is shown; Runnable=false marks a line the user can't send (a note, or a command
// they lack permission for), with Reason explaining why.
type consoleConfigCommand struct {
	Line     string `json:"line"`              // CLI text; empty for a comment-only note
	Comment  string `json:"comment,omitempty"` // optional note text
	Runnable bool   `json:"runnable"`
	Reason   string `json:"reason,omitempty"` // why it isn't runnable
}

// consoleConfigLocation reports what we know about the repeater's location, which
// the region commands depend on.
type consoleConfigLocation struct {
	Known         bool     `json:"known"`
	Lat           *float64 `json:"lat,omitempty"`
	Lon           *float64 `json:"lon,omitempty"`
	NeedsLocation bool     `json:"needsLocation"` // the org has regions but we have no coords
	RegionsCover  bool     `json:"regionsCover"`  // the location falls inside some org region
}

// consoleConfigJSON serves the recommended configuration for a repeater under a
// chosen org/profile. Location defaults to the repeater's stored coordinates; a
// ?lat=&lon= override lets the client preview a picked location before saving it.
func (s *Handlers) consoleConfigJSON(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := s.requireRepeaterAccess(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)

	orgs, err := s.Store.ListRepeaterConfigOrgs(ctx, id)
	if err != nil {
		s.ServerError(w, r, "could not load organizations", err)
		return
	}
	resp := consoleConfig{Orgs: orgs}
	if len(orgs) == 0 {
		writeConfigJSON(w, resp) // no config-bearing orgs for this repeater
		return
	}

	// Selected org: ?org= when it's one this repeater participates in, else the first.
	resp.SelectedOrg = orgs[0].OrgID
	if v := r.URL.Query().Get("org"); v != "" {
		if oid, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			for _, o := range orgs {
				if o.OrgID == oid {
					resp.SelectedOrg = oid
					break
				}
			}
		}
	}

	// Location: a ?lat=&lon= preview wins, otherwise the repeater's stored coords.
	lat, lon := rep.Latitude, rep.Longitude
	if qLat, qLon, okq := web.PreviewLatLon(r); okq {
		lat, lon = &qLat, &qLon
	}

	cv, err := web.BuildConfigView(ctx, s.Store, resp.SelectedOrg, r.URL.Query().Get("profile"), lat, lon)
	if err != nil {
		s.ServerError(w, r, "could not load configuration", err)
		return
	}
	resp.SelectedProfile = cv.Selected

	// Resolve each recommended line against the catalog + the user's sendable set so
	// the UI can mark which lines they may actually run.
	catalog, err := s.Store.ListCommands(ctx)
	if err != nil {
		s.ServerError(w, r, "could not load commands", err)
		return
	}
	sendable, err := s.Store.ListSendableCommandIDs(ctx, uid, id)
	if err != nil {
		s.ServerError(w, r, "could not load permissions", err)
		return
	}
	allowed := make(map[int64]bool, len(sendable))
	for _, cid := range sendable {
		allowed[cid] = true
	}
	runnable := func(line string) (bool, string) {
		cmd := resolveCommand(line, catalog)
		if cmd == nil {
			return false, "not a recognized command"
		}
		if !allowed[cmd.ID] {
			return false, "you don't have permission to run this"
		}
		return true, ""
	}

	// Profile base settings (verbatim; comment-only steps are notes, not commands),
	// with the location-derived region commands spliced at the profile's
	// {{ region }} marker or appended after all steps when it has none.
	//
	// AssembledLines owns that ordering, shared with the org Configuration page, so
	// what an owner previews there is exactly what the console offers to run.
	for _, l := range cv.AssembledLines() {
		if l.IsMarker {
			continue // a placeholder for the page; never sent to a device
		}
		c := consoleConfigCommand{}
		switch {
		case l.IsComment:
			c.Comment, c.Reason = l.Text, "note"
		default:
			c.Line = l.Text
			c.Runnable, c.Reason = runnable(l.Text)
		}
		resp.Commands = append(resp.Commands, c)
	}

	resp.Location = consoleConfigLocation{
		Known:         lat != nil && lon != nil,
		Lat:           lat,
		Lon:           lon,
		NeedsLocation: cv.HasRegions && (lat == nil || lon == nil),
		RegionsCover:  len(cv.RegionDef) > 0,
	}
	writeConfigJSON(w, resp)
}

func writeConfigJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// handleSetRepeaterLocation persists a location picked on the console map when the
// repeater has none (or the user is correcting it). Access-gated the same way as
// confirming — anyone who can operate the repeater can set its location.
func (s *Handlers) handleSetRepeaterLocation(w http.ResponseWriter, r *http.Request) {
	_, id, ok := s.requireRepeaterAccess(w, r)
	if !ok {
		return
	}
	lat, errLat := strconv.ParseFloat(r.FormValue("lat"), 64)
	lon, errLon := strconv.ParseFloat(r.FormValue("lon"), 64)
	if errLat != nil || errLon != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		http.Error(w, "invalid coordinates", http.StatusBadRequest)
		return
	}
	if err := s.Store.SetRepeaterLocation(r.Context(), id, lat, lon); err != nil {
		s.ServerError(w, r, "could not save location", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
