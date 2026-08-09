package core

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// pageOrgConfig renders an org's configuration read-only: a selected profile's
// base settings plus the regions (location steps). Visible to any signed-in user;
// admins get an Edit button. ?profile= chooses which profile to show; ?lat=&lon=
// marks which regions apply at a location. The root host serves it anonymously.
func (s *Handlers) pageOrgConfig(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		s.NotFound(w, r)
		return
	}
	org, err := s.Store.GetOrg(r.Context(), id)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	role, isMember, err := s.Store.OrgRole(r.Context(), id, uid)
	if err != nil {
		s.ServerError(w, r, "could not load organization", err)
		return
	}

	data := map[string]any{
		"Org": org,
		"Nav": s.OrgNavFor(r.Context(), web.OrgNavArgs{
			OrgID: id, Name: org.Name, Slug: org.Slug, Active: "config",
			IsMember: isMember, IsAdmin: role == "admin", Manage: isMember,
			CanGoToOrg: isMember, CanJoin: uid != 0 && !isMember,
		}),
		"CanEdit": role == "admin",
	}
	var latP, lonP *float64
	if lat, lon, ok := web.PreviewLatLon(r); ok {
		latP, lonP = &lat, &lon
		data["PreviewLat"], data["PreviewLon"] = lat, lon
	}
	cv, err := web.BuildConfigView(r.Context(), s.Store, id, r.URL.Query().Get("profile"), latP, lonP)
	if err != nil {
		s.ServerError(w, r, "could not load config", err)
		return
	}
	data["Config"] = cv
	s.Render(w, r, "org_config.html", data)
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
		s.NotFound(w, r)
		return
	}
	var pid int64
	var name, stepsText string
	if raw := chi.URLParam(r, "pid"); raw != "" {
		pid, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			s.NotFound(w, r)
			return
		}
		p, err := s.Store.GetProfile(r.Context(), orgID, pid)
		if errors.Is(err, store.ErrNotFound) {
			s.NotFound(w, r)
			return
		}
		if err != nil {
			s.ServerError(w, r, "could not load profile", err)
			return
		}
		name, stepsText = p.Name, stepsToText(p.Steps)
	}
	s.renderProfileEdit(w, r, org, pid, name, stepsText, nil, nil)
}

// renderProfileEdit renders the profile editor (shared by the initial GET and the
// error re-render). pid 0 means a new profile. An htmx request gets the modal
// fragment the Configuration page swaps in place; anything else (no JS, or a
// direct link) gets the standalone page.
func (s *Handlers) renderProfileEdit(w http.ResponseWriter, r *http.Request, org *store.Org, pid int64, name, stepsText string, errs, risky []string) {
	data := map[string]any{
		"Org":       org,
		"Nav":       s.orgAdminNav(r, org),
		"ProfileID": pid,
		"Action":    profileFormAction(org.Slug, pid),
		"Name":      name,
		"StepsText": stepsText,
		"Errors":    errs,
		"RiskyWarn": risky,
	}
	if r.Header.Get("HX-Request") != "" {
		data["Layout"] = "config-profile-modal"
	}
	s.Render(w, r, "config_profile_edit.html", data)
}

// profileFormAction is where the editor posts: the collection for a new profile,
// the profile's own URL for an update.
func profileFormAction(slug string, pid int64) string {
	if pid == 0 {
		return "/orgs/" + slug + "/config/profiles"
	}
	return "/orgs/" + slug + "/config/profiles/" + strconv.FormatInt(pid, 10)
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
		s.NotFound(w, r)
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
		s.NotFound(w, r)
		return
	}
	catalog, err := s.Store.ListCommands(r.Context())
	if err != nil {
		s.ServerError(w, r, "could not load commands", err)
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
			s.NotFound(w, r)
			return
		case err != nil:
			s.ServerError(w, r, "could not save profile", err)
			return
		default:
			// Back to the config page with the saved profile selected, so the editor
			// closes onto what was just changed. hxRedirect closes the modal.
			s.hxRedirect(w, r, "/orgs/"+orgParam(r)+"/config?profile="+url.QueryEscape(name))
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
		s.NotFound(w, r)
		return
	}
	if err := s.Store.DeleteProfile(r.Context(), orgID, pid); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.ServerError(w, r, "could not delete profile", err)
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r)+"/config", http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// parseConfigSteps turns textarea lines into config steps, validating each command
// against the catalog with the same resolver the console uses. Blank lines are
// skipped; "# ..." lines become comment steps; unknown commands are recorded in
// errs and risky commands in risky. It returns view steps (with rendered fields)
// and the store steps to persist.
func parseConfigSteps(text string, catalog []*store.Command, label string, errs, risky *[]string) (view, persist []store.ConfigStep) {
	markerSeen := false
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
		if isRegionMarkerLine(line) {
			if markerSeen {
				*errs = append(*errs, fmt.Sprintf("%s: the %s placeholder can only appear once.", label, store.RegionMarker))
				continue
			}
			markerSeen = true
			step := store.ConfigStep{CommandLine: store.RegionMarker}
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

// isRegionMarkerLine reports whether a profile line is the region placeholder,
// tolerating internal spacing and case (e.g. "{{region}}", "{{ REGION }}"). The
// canonical stored form is store.RegionMarker.
func isRegionMarkerLine(line string) bool {
	inner, ok := strings.CutPrefix(line, "{{")
	if !ok {
		return false
	}
	inner, ok = strings.CutSuffix(inner, "}}")
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(inner), "region")
}

// stepsToText renders stored steps back into editable textarea content: commands
// (and the region marker) as-is, comment steps prefixed with "# ".
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
