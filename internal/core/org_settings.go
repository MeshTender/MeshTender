package core

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MeshTender/MeshTender/internal/ingest"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// The org Settings tab (admins only), in the order of its left rail
// (org-settings-nav). A singular thing — the org's profile, links and radio, the
// MQTT broker — is a form on its section's page; a list — regions, configuration
// profiles, observers, nets — has a New button and per-row actions, with creating
// and editing on pages of their own.
var settingsSections = []string{"profile", "members", "radio", "channels", "regions", "profiles", "mqtt", "nets"}

// settingsURL is a Settings section's page; "profile" is the tab's landing page.
func settingsURL(slug, section string) string {
	if section == "profile" {
		return "/orgs/" + slug + "/settings"
	}
	return "/orgs/" + slug + "/settings/" + section
}

// requireSettings resolves the org for a Settings page, writing a 404 for anyone
// but an org admin. ok is false when a response was written.
func (s *Handlers) requireSettings(w http.ResponseWriter, r *http.Request) (*store.Org, bool) {
	org, isAdmin, ok := s.orgMemberPage(w, r)
	if !ok {
		return nil, false
	}
	if !isAdmin {
		s.NotFound(w, r)
		return nil, false
	}
	return org, true
}

// pageOrgSettings renders one Settings section.
func (s *Handlers) pageOrgSettings(w http.ResponseWriter, r *http.Request) {
	section := chi.URLParam(r, "section")
	if section == "" {
		section = "profile"
	}
	if !slices.Contains(settingsSections, section) {
		s.NotFound(w, r)
		return
	}
	org, ok := s.requireSettings(w, r)
	if !ok {
		return
	}
	s.renderSettings(w, r, org, section, nil)
}

// observerFilter reads the observer list's ?observers=, defaulting to the
// approved ones: those are the org's, the rest were only discovered.
func observerFilter(v string) store.ObserverFilter {
	switch f := store.ObserverFilter(v); f {
	case store.ObserversUnapproved, store.ObserversAll:
		return f
	}
	return store.ObserversApproved
}

// observerFilterOption is one option of the observer list's filter.
type observerFilterOption struct {
	Value store.ObserverFilter
	Label string
	Count int
}

// pageNewObserver and pageNewNet are the "New" pages of their lists.
func (s *Handlers) pageNewObserver(w http.ResponseWriter, r *http.Request) {
	if org, ok := s.requireSettings(w, r); ok {
		s.renderSettings(w, r, org, "mqtt", map[string]any{"New": true})
	}
}

// pageObserver approves a discovered observer, or renames an approved one.
func (s *Handlers) pageObserver(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireSettings(w, r)
	if !ok {
		return
	}
	oid, err := strconv.ParseInt(chi.URLParam(r, "oid"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	o, err := s.Store.GetOrgObserver(r.Context(), org.ID, oid)
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't load the observer.", err)
		return
	}
	s.renderSettings(w, r, org, "mqtt", map[string]any{"ObserverEdit": o, "From": string(observerFilter(r.URL.Query().Get("from")))})
}

func (s *Handlers) pageNewNet(w http.ResponseWriter, r *http.Request) {
	if org, ok := s.requireSettings(w, r); ok {
		s.renderSettings(w, r, org, "nets", map[string]any{"New": true})
	}
}

// renderSettings renders a Settings section, with extra merged over its data —
// e.g. a form's submitted values and error when it's re-shown.
func (s *Handlers) renderSettings(w http.ResponseWriter, r *http.Request, org *store.Org, section string, extra map[string]any) {
	ctx := r.Context()
	data, err := s.settingsData(ctx, r, org, section)
	if err != nil {
		s.ServerError(w, r, "Couldn't load settings.", err)
		return
	}
	for k, v := range extra {
		data[k] = v
	}
	s.Render(w, r, "org_settings.html", data)
}

// settingsData loads what a Settings section shows.
func (s *Handlers) settingsData(ctx context.Context, r *http.Request, org *store.Org, section string) (map[string]any, error) {
	data := map[string]any{
		"Org":     org,
		"Nav":     s.orgNav(ctx, org, "settings-"+section, true),
		"Section": section,
	}
	switch section {
	case "profile":
		links, err := s.Store.ListOrgLinks(ctx, org.ID)
		if err != nil {
			return nil, err
		}
		data["ProfileForm"] = store.OrgProfile{Slug: org.Slug, Name: org.Name, Summary: org.Summary, Description: org.Description, Region: org.Region}
		data["MaxOrgSummary"] = store.MaxOrgSummary
		data["Links"] = links
		data["Platforms"] = store.LinkPlatforms()
		data["PlatformsJS"] = web.LinkPlatformsJS(store.LinkPlatforms())
	case "members":
		members, err := s.Store.ListOrgMembers(ctx, org.ID)
		if err != nil {
			return nil, err
		}
		data["Members"] = members
		data["Self"] = s.Auth.CurrentUserID(ctx)
	case "radio":
		mesh, err := s.Store.OrgMesh(ctx, org.ID)
		if err != nil {
			return nil, err
		}
		shown := defaultRadio()
		if mesh.Radio != nil {
			shown = *mesh.Radio
			data["Preset"] = mesh.Radio.PresetName()
		}
		data["RadioSet"] = mesh.Radio != nil
		data["RadioFields"] = newRadioFields(shown, true)
		data["PathHashModes"] = []int{0, 1, 2}
		if mesh.PathHashMode != nil {
			data["PathHashSet"], data["PathHashMode"] = true, *mesh.PathHashMode
		}
	case "channels":
		channels, err := s.Store.ListOrgChannels(ctx, org.ID)
		if err != nil {
			return nil, err
		}
		regions, err := s.Store.ListRegions(ctx, org.ID)
		if err != nil {
			return nil, err
		}
		rows := make([]channelRow, len(channels))
		for i, c := range channels {
			rows[i].OrgChannel = c
			rows[i].Scope, _ = web.ChannelScope(c, regions)
		}
		data["Channels"] = rows
		data["Regions"] = regions
		data["CanAdd"] = len(channels) < store.MaxOrgChannels
	case "regions", "profiles":
		cv, err := web.BuildConfigView(ctx, s.Store, org.ID, r.URL.Query().Get("profile"), nil, nil)
		if err != nil {
			return nil, err
		}
		cv.HideMapHint = true
		data["Config"] = cv
	case "mqtt":
		broker, err := s.Store.GetOrgBroker(ctx, org.ID)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return nil, err
		default:
			data["Broker"] = broker
			data["BrokerError"] = ingest.CategoryMessage(broker.LastError)
		}
		filter := observerFilter(r.URL.Query().Get("observers"))
		observers, err := s.Store.ListOrgObservers(ctx, org.ID, filter)
		if err != nil {
			return nil, err
		}
		counts, err := s.Store.CountOrgObservers(ctx, org.ID)
		if err != nil {
			return nil, err
		}
		data["Observers"] = observers
		data["ObserverFilter"] = string(filter)
		data["ObserverFilters"] = []observerFilterOption{
			{store.ObserversApproved, "Approved", counts.Approved},
			{store.ObserversUnapproved, "Unapproved", counts.Unapproved},
			{store.ObserversAll, "All", counts.Approved + counts.Unapproved},
		}
		data["Now"] = time.Now()
	case "nets":
		now := time.Now()
		nets, err := s.Store.ListOrgNetSummaries(ctx, org.ID, now)
		if err != nil {
			return nil, err
		}
		data["Nets"] = nets
		data["Now"] = now
		data["CanAdd"] = len(nets) < store.MaxNetsPerOrg
		data["Form"] = s.netFormDefaults(r)
		data["Weekdays"] = weekdays
	}
	return data, nil
}

// redirectToObserverSettings keeps the old /orgs/{id}/observers address working:
// the broker and observers now live under Settings › MQTT.
func (s *Handlers) redirectToObserverSettings(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, settingsURL(orgParam(r), "mqtt"), http.StatusMovedPermanently) //nolint:gosec // G710: local path
}
