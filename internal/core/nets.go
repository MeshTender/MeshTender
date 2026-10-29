package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// Check-in nets: a weekly window in which members check in by sending a flood
// advert. Members see the nets and their rosters; admins schedule them.

const maxNetDescription = 2000

// weekdays orders the weekday picker Sunday-first, matching time.Weekday.
var weekdays = []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday}

// netSchedule describes a net's recurrence in its own zone, in the same 12-hour
// style as every timestamp, e.g. "Sundays, 7:00–8:00 PM, New York time" or, when
// it crosses noon or midnight, "Fridays, 11:00 PM–1:00 AM, …". Session times
// elsewhere are shown in the viewer's zone; this line is the net's definition.
func netSchedule(n *store.Net) string {
	start := time.Date(2000, 1, 1, n.StartMinute/60, n.StartMinute%60, 0, 0, time.UTC)
	end := start.Add(time.Duration(n.DurationMinutes) * time.Minute)
	from := start.Format("3:04 PM")
	if start.Format("PM") == end.Format("PM") {
		from = start.Format("3:04")
	}
	return fmt.Sprintf("%ss, %s–%s, %s", n.Weekday, from, end.Format("3:04 PM"), zoneName(n.Timezone))
}

// zoneName is an IANA zone as people say it: "America/New_York" → "New York
// time". UTC and anything without a city stay as they are.
func zoneName(tz string) string {
	i := strings.LastIndex(tz, "/")
	if i < 0 {
		return tz
	}
	return strings.ReplaceAll(tz[i+1:], "_", " ") + " time"
}

// parseNetForm validates the create/edit form. msg is a user-facing error.
func parseNetForm(r *http.Request) (in store.NetInput, msg string) {
	in.Name = strings.TrimSpace(r.FormValue("name"))
	if in.Name == "" || utf8.RuneCountInString(in.Name) > maxCompanionName {
		return in, "Give the net a name (up to 64 characters)."
	}
	in.Description = strings.TrimSpace(r.FormValue("description"))
	if utf8.RuneCountInString(in.Description) > maxNetDescription {
		return in, "The description is too long."
	}
	wd, err := strconv.Atoi(r.FormValue("weekday"))
	if err != nil || wd < 0 || wd > 6 {
		return in, "Choose a day of the week."
	}
	in.Weekday = time.Weekday(wd)
	start, err := time.Parse("15:04", r.FormValue("start"))
	if err != nil {
		return in, "Enter a start time like 19:00."
	}
	in.StartMinute = start.Hour()*60 + start.Minute()
	in.DurationMinutes, err = strconv.Atoi(r.FormValue("duration"))
	if err != nil || in.DurationMinutes < 1 || in.DurationMinutes > 24*60 {
		return in, "The check-in window must be between 1 minute and 24 hours."
	}
	in.Timezone = strings.TrimSpace(r.FormValue("timezone"))
	if in.Timezone == "" || !web.ValidTimeZone(in.Timezone) {
		return in, "Choose the net's time zone."
	}
	in.Active = r.FormValue("active") == "1"
	in.ManualAck = r.FormValue("ack") == "manual"
	return in, ""
}

func netsURL(r *http.Request) string { return "/orgs/" + orgParam(r) + "/nets" }

// netFormDefaults pre-fills the create form: the viewer's saved zone, Sunday
// evening, an hour long.
func (s *Handlers) netFormDefaults(r *http.Request) store.NetInput {
	in := store.NetInput{Weekday: time.Sunday, StartMinute: 19 * 60, DurationMinutes: 60, Active: true}
	if u, err := s.Store.GetUserByID(r.Context(), s.Auth.CurrentUserID(r.Context())); err == nil {
		in.Timezone = u.Timezone
	}
	return in
}

// netSummaryView is a net on the list page.
type netSummaryView struct {
	store.NetSummary
	Schedule string
}

func (s *Handlers) pageOrgNets(w http.ResponseWriter, r *http.Request) {
	org, isAdmin, ok := s.orgMemberPage(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	now := time.Now()
	views, err := s.netSummaryViews(ctx, org.ID, now)
	if err != nil {
		s.ServerError(w, r, "Couldn't load nets.", err)
		return
	}
	data := map[string]any{
		"Org":     org,
		"Nav":     s.orgNav(ctx, org, "nets", isAdmin),
		"IsAdmin": isAdmin,
		"Nets":    views,
		"Now":     now,
	}
	if err := s.companionActivity(r, org.ID, data); err != nil {
		s.ServerError(w, r, "Couldn't load activity.", err)
		return
	}
	s.Render(w, r, "org_nets.html", data)
}

// netSummaryViews loads an org's nets for the "net-list" partial.
func (s *Handlers) netSummaryViews(ctx context.Context, orgID int64, now time.Time) ([]netSummaryView, error) {
	nets, err := s.Store.ListOrgNetSummaries(ctx, orgID, now)
	if err != nil {
		return nil, err
	}
	views := make([]netSummaryView, len(nets))
	for i := range nets {
		views[i] = netSummaryView{NetSummary: nets[i], Schedule: netSchedule(&nets[i].Net)}
	}
	return views, nil
}

func (s *Handlers) handleCreateNet(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	in, msg := parseNetForm(r)
	if msg != "" {
		web.RedirectErr(w, r, settingsURL(orgParam(r), "nets")+"/new", msg)
		return
	}
	in.Active = true // a new net runs; its settings page can pause it
	net, err := s.Store.CreateNet(r.Context(), id, in, s.Auth.CurrentUserID(r.Context()), time.Now())
	if errors.Is(err, store.ErrNetLimit) {
		web.RedirectErr(w, r, settingsURL(orgParam(r), "nets"), "This organization has the maximum number of nets.")
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't create the net.", err)
		return
	}
	web.RedirectFlash(w, r, netSettingsURL(orgParam(r), net.ID), "ok",
		"Net scheduled. Choose where net control's messages go out under On the air.")
}

// netSettingsURL is a net's page in Settings › Nets.
func netSettingsURL(slug string, netID int64) string {
	return settingsURL(slug, "nets") + "/" + strconv.FormatInt(netID, 10)
}

// orgNet loads the net named in the URL for a member page, writing a 404 when
// it isn't the org's.
func (s *Handlers) orgNet(w http.ResponseWriter, r *http.Request, orgID int64) (*store.Net, bool) {
	nid, err := strconv.ParseInt(chi.URLParam(r, "nid"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return nil, false
	}
	net, err := s.Store.GetNet(r.Context(), orgID, nid)
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return nil, false
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't load the net.", err)
		return nil, false
	}
	return net, true
}

// pageOrgNet is a net's overview, for every member: its schedule, how it runs
// and where it's on the air, the session in progress or next, and past
// sessions. Admins change it in Settings › Nets.
func (s *Handlers) pageOrgNet(w http.ResponseWriter, r *http.Request) {
	org, isAdmin, ok := s.orgMemberPage(w, r)
	if !ok {
		return
	}
	net, ok := s.orgNet(w, r, org.ID)
	if !ok {
		return
	}
	ctx := r.Context()
	now := time.Now()
	var before *time.Time
	if b, ok := web.DecodeCursor[time.Time](r.URL.Query().Get("before")); ok && !b.IsZero() {
		before = &b
	}
	sessions, hasMore, err := s.Store.ListNetSessionsPage(ctx, org.ID, net.ID, now, before)
	if err != nil {
		s.ServerError(w, r, "Couldn't load sessions.", err)
		return
	}
	next, err := s.Store.NextNetSession(ctx, net.ID, now)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.ServerError(w, r, "Couldn't load the next session.", err)
		return
	}
	var live int
	if next != nil && !now.Before(next.StartsAt) {
		counts, err := s.Store.NetSessionsCheckedIn(ctx, org.ID, []int64{next.ID})
		if err != nil {
			s.ServerError(w, r, "Couldn't load the session.", err)
			return
		}
		live = counts[next.ID]
	}
	controls, err := s.Store.ListNetControls(ctx, org.ID, net.ID)
	if err != nil {
		s.ServerError(w, r, "Couldn't load net control.", err)
		return
	}
	air, err := s.Store.GetNetAir(ctx, org.ID, net.ID)
	if err != nil {
		s.ServerError(w, r, "Couldn't load the on-air settings.", err)
		return
	}
	data := map[string]any{
		"Org":         org,
		"Nav":         s.orgNav(ctx, org, "nets", isAdmin),
		"IsAdmin":     isAdmin,
		"Net":         net,
		"Controls":    controls,
		"Air":         netAirView{NetAir: air},
		"Schedule":    netSchedule(net),
		"Next":        next,
		"LiveCount":   live,
		"Now":         now,
		"Sessions":    sessions,
		"SettingsURL": netSettingsURL(org.Slug, net.ID),
	}
	if hasMore && len(sessions) > 0 {
		data["NextBefore"] = web.EncodeCursor(sessions[len(sessions)-1].StartsAt)
	}
	s.Render(w, r, "org_net.html", data)
}

// pageNetSettings is a net's page in Settings › Nets: its schedule, how
// check-ins are acknowledged and by whom, its on-air settings, and deleting it.
func (s *Handlers) pageNetSettings(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireSettings(w, r)
	if !ok {
		return
	}
	if net, ok := s.orgNet(w, r, org.ID); ok {
		s.renderNetSettings(w, r, org, net, nil)
	}
}

// renderNetSettings renders a net's settings page, with extra merged over its
// data — a section's submitted values and error when it's re-shown.
func (s *Handlers) renderNetSettings(w http.ResponseWriter, r *http.Request, org *store.Org, net *store.Net, extra map[string]any) {
	ctx := r.Context()
	data, err := s.settingsData(ctx, r, org, "nets")
	if err != nil {
		s.ServerError(w, r, "Couldn't load settings.", err)
		return
	}
	controls, err := s.Store.ListNetControls(ctx, org.ID, net.ID)
	if err != nil {
		s.ServerError(w, r, "Couldn't load net control.", err)
		return
	}
	// Candidates for net control: members not already designated. Admins can
	// act as net control anyway, so they're left out.
	members, err := s.Store.ListOrgMembers(ctx, org.ID)
	if err != nil {
		s.ServerError(w, r, "Couldn't load members.", err)
		return
	}
	designated := map[int64]bool{}
	for _, c := range controls {
		designated[c.UserID] = true
	}
	var candidates []store.OrgMemberInfo
	for _, m := range members {
		if m.Role != "admin" && !designated[m.UserID] {
			candidates = append(candidates, m)
		}
	}
	air, err := s.Store.GetNetAir(ctx, org.ID, net.ID)
	if err != nil {
		s.ServerError(w, r, "Couldn't load the on-air settings.", err)
		return
	}
	regions, err := s.Store.ListRegions(ctx, org.ID)
	if err != nil {
		s.ServerError(w, r, "Couldn't load regions.", err)
		return
	}
	radio, err := s.Store.OrgRadio(ctx, org.ID)
	if err != nil {
		s.ServerError(w, r, "Couldn't load the radio settings.", err)
		return
	}
	data["NetEdit"] = true
	data["Net"] = net
	data["Form"] = net.NetInput
	data["Weekdays"] = weekdays
	data["Controls"] = controls
	data["Candidates"] = candidates
	data["Air"] = netAirView{NetAir: air, Regions: regions}
	data["Radio"] = radio
	data["NetURL"] = netSettingsURL(org.Slug, net.ID)
	for k, v := range extra {
		data[k] = v
	}
	s.Render(w, r, "org_settings.html", data)
}

// settingsNet resolves the org and net for a net settings action, writing a
// 404 for anyone but an org admin. ok is false when a response was written.
func (s *Handlers) settingsNet(w http.ResponseWriter, r *http.Request) (*store.Org, *store.Net, bool) {
	org, ok := s.requireSettings(w, r)
	if !ok {
		return nil, nil, false
	}
	net, ok := s.orgNet(w, r, org.ID)
	return org, net, ok
}

func netURL(r *http.Request) string { return netsURL(r) + "/" + chi.URLParam(r, "nid") }

func (s *Handlers) handleAddNetControl(w http.ResponseWriter, r *http.Request) {
	org, net, ok := s.settingsNet(w, r)
	if !ok {
		return
	}
	back := netSettingsURL(org.Slug, net.ID)
	uid, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil {
		web.RedirectErr(w, r, back, "Choose a member.")
		return
	}
	err = s.Store.AddNetControl(r.Context(), org.ID, net.ID, uid, s.Auth.CurrentUserID(r.Context()))
	if errors.Is(err, store.ErrNotFound) {
		web.RedirectErr(w, r, back, "That member isn't in this organization.")
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't add net control.", err)
		return
	}
	http.Redirect(w, r, back+"#net-control", http.StatusSeeOther) //nolint:gosec // G710: local path
}

func (s *Handlers) handleRemoveNetControl(w http.ResponseWriter, r *http.Request) {
	org, net, ok := s.settingsNet(w, r)
	if !ok {
		return
	}
	uid, err := strconv.ParseInt(chi.URLParam(r, "uid"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	if err := s.Store.RemoveNetControl(r.Context(), org.ID, net.ID, uid); err != nil {
		s.ServerError(w, r, "Couldn't remove net control.", err)
		return
	}
	http.Redirect(w, r, netSettingsURL(org.Slug, net.ID)+"#net-control", http.StatusSeeOther) //nolint:gosec // G710: local path
}

// handleUpdateNet saves a net's name, description and schedule. A rejected
// form re-shows what was entered with the error.
func (s *Handlers) handleUpdateNet(w http.ResponseWriter, r *http.Request) {
	org, net, ok := s.settingsNet(w, r)
	if !ok {
		return
	}
	in, msg := parseNetForm(r)
	if msg != "" {
		s.renderNetSettings(w, r, org, net, map[string]any{"Form": in, "ScheduleError": msg})
		return
	}
	err := s.Store.UpdateNet(r.Context(), org.ID, net.ID, in, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't save the net.", err)
		return
	}
	web.RedirectOK(w, r, netSettingsURL(org.Slug, net.ID), "Schedule saved. Sessions that have already started keep their original times.")
}

// handleSetNetAck saves how a net's check-ins are acknowledged.
func (s *Handlers) handleSetNetAck(w http.ResponseWriter, r *http.Request) {
	org, net, ok := s.settingsNet(w, r)
	if !ok {
		return
	}
	if err := s.Store.SetNetManualAck(r.Context(), org.ID, net.ID, r.FormValue("ack") == "manual"); errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	} else if err != nil {
		s.ServerError(w, r, "Couldn't save the net.", err)
		return
	}
	web.RedirectOK(w, r, netSettingsURL(org.Slug, net.ID), "Check-in settings saved.")
}

func (s *Handlers) handleDeleteNet(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	nid, err := strconv.ParseInt(chi.URLParam(r, "nid"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	if err := s.Store.DeleteNet(r.Context(), id, nid); errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	} else if err != nil {
		s.ServerError(w, r, "Couldn't delete the net.", err)
		return
	}
	web.RedirectOK(w, r, settingsURL(orgParam(r), "nets"), "Net deleted.")
}

// netSessionContext is what every session page and action resolves first.
type netSessionContext struct {
	org        *store.Org
	isAdmin    bool
	net        *store.Net
	session    *store.NetSession
	netControl bool // may acknowledge and add check-ins
}

// loadNetSession resolves the org, net and started session in the URL for a
// member, and whether they may act as net control (an org admin, or designated
// for this net). ok is false when a response was written.
func (s *Handlers) loadNetSession(w http.ResponseWriter, r *http.Request) (netSessionContext, bool) {
	org, isAdmin, ok := s.orgMemberPage(w, r)
	if !ok {
		return netSessionContext{}, false
	}
	net, ok := s.orgNet(w, r, org.ID)
	if !ok {
		return netSessionContext{}, false
	}
	ctx := r.Context()
	sid, err := strconv.ParseInt(chi.URLParam(r, "sid"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return netSessionContext{}, false
	}
	sess, err := s.Store.GetNetSession(ctx, org.ID, net.ID, sid)
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return netSessionContext{}, false
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't load the session.", err)
		return netSessionContext{}, false
	}
	if sess.StartsAt.After(time.Now()) {
		// A session that hasn't started has nothing to show or acknowledge.
		s.NotFound(w, r)
		return netSessionContext{}, false
	}
	nc := isAdmin
	if !nc {
		if nc, err = s.Store.IsNetControl(ctx, org.ID, net.ID, s.Auth.CurrentUserID(ctx)); err != nil {
			s.ServerError(w, r, "Couldn't check net control.", err)
			return netSessionContext{}, false
		}
	}
	return netSessionContext{org: org, isAdmin: isAdmin, net: net, session: sess, netControl: nc}, true
}

func sessionURL(r *http.Request) string {
	return netURL(r) + "/sessions/" + chi.URLParam(r, "sid")
}

// handleAcknowledge records net control acknowledging a member for the session:
// a heard advert they've replied to, or a check-in logged by hand (with an
// optional note, e.g. "with traffic").
func (s *Handlers) handleAcknowledge(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadNetSession(w, r)
	if !ok {
		return
	}
	if !c.netControl {
		s.NotFound(w, r)
		return
	}
	member, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil {
		web.RedirectErr(w, r, sessionReturnURL(r), "Choose a member.")
		return
	}
	err = s.Store.AcknowledgeMember(r.Context(), c.org.ID, c.session.ID, member, s.Auth.CurrentUserID(r.Context()),
		strings.TrimSpace(r.FormValue("note")), time.Now())
	if errors.Is(err, store.ErrNotFound) {
		web.RedirectErr(w, r, sessionReturnURL(r), "That member isn't in this organization.")
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't record the acknowledgement.", err)
		return
	}
	http.Redirect(w, r, sessionReturnURL(r), http.StatusSeeOther) //nolint:gosec // G710: local path
}

func (s *Handlers) handleRemoveAcknowledge(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadNetSession(w, r)
	if !ok {
		return
	}
	if !c.netControl {
		s.NotFound(w, r)
		return
	}
	member, err := strconv.ParseInt(chi.URLParam(r, "uid"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	if err := s.Store.RemoveAcknowledgement(r.Context(), c.org.ID, c.session.ID, member); errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	} else if err != nil {
		s.ServerError(w, r, "Couldn't undo the acknowledgement.", err)
		return
	}
	http.Redirect(w, r, sessionReturnURL(r), http.StatusSeeOther) //nolint:gosec // G710: local path
}
