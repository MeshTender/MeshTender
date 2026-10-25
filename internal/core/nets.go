package core

import (
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

// netSchedule describes a net's recurrence in its own zone, e.g.
// "Sundays, 19:00–20:00 (America/New_York)". Session times elsewhere are shown
// in the viewer's zone; this line is the net's definition.
func netSchedule(n *store.Net) string {
	start := time.Date(2000, 1, 1, n.StartMinute/60, n.StartMinute%60, 0, 0, time.UTC)
	end := start.Add(time.Duration(n.DurationMinutes) * time.Minute)
	return fmt.Sprintf("%ss, %s–%s (%s)", n.Weekday, start.Format("15:04"), end.Format("15:04"), n.Timezone)
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
	nets, err := s.Store.ListOrgNetSummaries(ctx, org.ID, now)
	if err != nil {
		s.ServerError(w, r, "Couldn't load nets.", err)
		return
	}
	views := make([]netSummaryView, len(nets))
	for i := range nets {
		views[i] = netSummaryView{NetSummary: nets[i], Schedule: netSchedule(&nets[i].Net)}
	}
	s.Render(w, r, "org_nets.html", map[string]any{
		"Org":      org,
		"Nav":      s.orgNav(ctx, org, "nets", isAdmin),
		"IsAdmin":  isAdmin,
		"Nets":     views,
		"Now":      now,
		"CanAdd":   len(nets) < store.MaxNetsPerOrg,
		"Form":     s.netFormDefaults(r),
		"Weekdays": weekdays,
		"Error":    r.URL.Query().Get("error"),
	})
}

func (s *Handlers) handleCreateNet(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	in, msg := parseNetForm(r)
	if msg != "" {
		web.RedirectErr(w, r, netsURL(r), msg)
		return
	}
	net, err := s.Store.CreateNet(r.Context(), id, in, s.Auth.CurrentUserID(r.Context()), time.Now())
	if errors.Is(err, store.ErrNetLimit) {
		web.RedirectErr(w, r, netsURL(r), "This organization has the maximum number of nets.")
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't create the net.", err)
		return
	}
	http.Redirect(w, r, netsURL(r)+"/"+strconv.FormatInt(net.ID, 10), http.StatusSeeOther) //nolint:gosec // G710: local path
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
	data := map[string]any{
		"Org":      org,
		"Nav":      s.orgNav(ctx, org, "nets", isAdmin),
		"IsAdmin":  isAdmin,
		"Net":      net,
		"Schedule": netSchedule(net),
		"Next":     next,
		"Now":      now,
		"Sessions": sessions,
		"Form":     net.NetInput,
		"Weekdays": weekdays,
		"Error":    r.URL.Query().Get("error"),
		"OK":       r.URL.Query().Get("ok"),
	}
	if hasMore && len(sessions) > 0 {
		data["NextBefore"] = web.EncodeCursor(sessions[len(sessions)-1].StartsAt)
	}
	s.Render(w, r, "org_net.html", data)
}

func (s *Handlers) handleUpdateNet(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	nid, err := strconv.ParseInt(chi.URLParam(r, "nid"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	netURL := netsURL(r) + "/" + strconv.FormatInt(nid, 10)
	in, msg := parseNetForm(r)
	if msg != "" {
		web.RedirectErr(w, r, netURL, msg)
		return
	}
	err = s.Store.UpdateNet(r.Context(), id, nid, in, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't save the net.", err)
		return
	}
	web.RedirectFlash(w, r, netURL, "ok", "Saved. Sessions that have already started keep their original times.")
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
	http.Redirect(w, r, netsURL(r), http.StatusSeeOther) //nolint:gosec // G710: local path
}

func (s *Handlers) pageNetSession(w http.ResponseWriter, r *http.Request) {
	org, isAdmin, ok := s.orgMemberPage(w, r)
	if !ok {
		return
	}
	net, ok := s.orgNet(w, r, org.ID)
	if !ok {
		return
	}
	ctx := r.Context()
	sid, err := strconv.ParseInt(chi.URLParam(r, "sid"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	sess, err := s.Store.GetNetSession(ctx, org.ID, net.ID, sid)
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't load the session.", err)
		return
	}
	now := time.Now()
	if sess.StartsAt.After(now) {
		// A roster for a session that hasn't started would only ever say "not
		// heard" for everyone.
		s.NotFound(w, r)
		return
	}
	roster, err := s.Store.NetSessionRoster(ctx, org.ID, sess)
	if err != nil {
		s.ServerError(w, r, "Couldn't load the roster.", err)
		return
	}
	observers, err := s.Store.ListOrgObservers(ctx, org.ID)
	if err != nil {
		s.ServerError(w, r, "Couldn't load observers.", err)
		return
	}
	online := 0
	for _, o := range observers {
		if o.OnlineAt(now) {
			online++
		}
	}
	checkedIn := 0
	for _, e := range roster {
		if e.CheckedIn() {
			checkedIn++
		}
	}
	s.Render(w, r, "net_session.html", map[string]any{
		"Org":             org,
		"Nav":             s.orgNav(ctx, org, "nets", isAdmin),
		"Net":             net,
		"Session":         sess,
		"InProgress":      !now.Before(sess.StartsAt) && now.Before(sess.EndsAt),
		"Roster":          roster,
		"CheckedIn":       checkedIn,
		"Observers":       len(observers),
		"ObserversOnline": online,
	})
}
