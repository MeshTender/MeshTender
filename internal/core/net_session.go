package core

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/MeshTender/MeshTender/internal/mesh"
	"github.com/MeshTender/MeshTender/internal/netair"
	"github.com/MeshTender/MeshTender/internal/store"
)

// A net session's page: for net control during a live session, a queue of
// check-ins waiting for acknowledgement, the session log, and a rail with the
// modem and adding a check-in by hand; members see the log.

// Session log filters (?log=).
const (
	logAll      = ""
	logCheckIns = "checkins"
	logNews     = "news"
)

// memberSearchLimit caps "Add a check-in" search results.
const memberSearchLimit = 8

// sessionLogEntry is one line of a session's log: a member's check-in or a
// channel message, at the time it happened.
type sessionLogEntry struct {
	At      time.Time
	CheckIn *store.RosterEntry
	Message *store.NetSessionMessage
}

// sessionLog merges the roster's check-ins and the channel messages into one
// log, newest first, keeping what filter asks for. Both inputs are bounded:
// the roster by the org's membership, messages by MaxNetSessionMessages.
func sessionLog(roster []store.RosterEntry, messages []store.NetSessionMessage, filter string) []sessionLogEntry {
	var log []sessionLogEntry
	if filter != logNews {
		for i := range roster {
			e := &roster[i]
			switch {
			case e.Heard():
				log = append(log, sessionLogEntry{At: *e.HeardAt, CheckIn: e})
			case e.Acked():
				log = append(log, sessionLogEntry{At: *e.AckedAt, CheckIn: e})
			}
		}
	}
	if filter != logCheckIns {
		for i := range messages {
			log = append(log, sessionLogEntry{At: messages[i].HeardAt, Message: &messages[i]})
		}
	}
	slices.SortStableFunc(log, func(a, b sessionLogEntry) int { return b.At.Compare(a.At) })
	return log
}

// logFilter reads ?log=, defaulting to everything.
func logFilter(r *http.Request) string {
	switch f := r.URL.Query().Get("log"); f {
	case logCheckIns, logNews:
		return f
	}
	return logAll
}

func (s *Handlers) pageNetSession(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadNetSession(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	now := time.Now()
	fail := func(msg string, err error) { s.ServerError(w, r, msg, err) }
	roster, err := s.Store.NetSessionRoster(ctx, c.org.ID, c.session)
	if err != nil {
		fail("Couldn't load the roster.", err)
		return
	}
	messages, err := s.Store.ListNetSessionMessages(ctx, c.session.ID)
	if err != nil {
		fail("Couldn't load the channel log.", err)
		return
	}
	members, _, err := s.Store.OrgCounts(ctx, c.org.ID)
	if err != nil {
		fail("Couldn't load the organization.", err)
		return
	}
	observers, err := s.Store.ListOrgObservers(ctx, c.org.ID)
	if err != nil {
		fail("Couldn't load observers.", err)
		return
	}
	online := 0
	for _, o := range observers {
		if o.OnlineAt(now) {
			online++
		}
	}
	checkedIn := 0
	var waiting []store.RosterEntry
	for _, e := range roster {
		if e.CheckedIn() {
			checkedIn++
		}
		if c.net.ManualAck && e.Heard() && !e.Acked() {
			waiting = append(waiting, e) // the roster is in check-in order
		}
	}
	filter := logFilter(r)
	pollURL := sessionURL(r)
	if filter != logAll {
		pollURL += "?log=" + filter
	}
	inProgress := !now.Before(c.session.StartsAt) && now.Before(c.session.EndsAt)
	data := map[string]any{
		"Org":             c.org,
		"Nav":             s.orgNav(ctx, c.org, "nets", c.isAdmin),
		"IsAdmin":         c.isAdmin,
		"Net":             c.net,
		"Session":         c.session,
		"SessionURL":      sessionURL(r),
		"PollURL":         pollURL,
		"InProgress":      inProgress,
		"NetControl":      c.netControl,
		"Log":             sessionLog(roster, messages, filter),
		"LogFilter":       filter,
		"Waiting":         waiting,
		"CheckedIn":       checkedIn,
		"Members":         members,
		"Observers":       len(observers),
		"ObserversOnline": online,
	}
	if c.netControl && inProgress {
		air, err := s.Store.GetNetAir(ctx, c.org.ID, c.net.ID)
		if err != nil {
			fail("Couldn't load the on-air settings.", err)
			return
		}
		radio, err := s.Store.OrgRadio(ctx, c.org.ID)
		if err != nil {
			fail("Couldn't load the radio settings.", err)
			return
		}
		sender := netair.Sender(air, c.net.Name)
		data["Modem"] = map[string]any{
			"WSPath":  sessionURL(r) + "/modem/ws",
			"Channel": channelLabel(air),
			"Sender":  sender,
			"SayMax":  mesh.GroupTextRoom(sender),
			"Radio":   radio,
			"OrgSlug": c.org.Slug,
		}
	}
	// The in-progress page polls itself (htmx) so net control sees adverts
	// arrive; that request gets just the live part.
	if r.Header.Get("HX-Request") != "" {
		data["Layout"] = "session-live"
	}
	s.Render(w, r, "net_session.html", data)
}

// memberMatch is an "Add a check-in" search result: a member and where they
// stand in the session.
type memberMatch struct {
	store.OrgMemberInfo
	// Entry is the member's roster row; nil when they have no confirmed
	// companion and haven't been added by hand.
	Entry *store.RosterEntry
}

// CheckedIn reports whether the member is already checked in, so can't be added.
func (m memberMatch) CheckedIn() bool { return m.Entry != nil && m.Entry.CheckedIn() }

// pageSessionMemberSearch answers net control's "Add a check-in" search with
// the matching members and their status (an htmx fragment).
func (s *Handlers) pageSessionMemberSearch(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadNetSession(w, r)
	if !ok {
		return
	}
	if !c.netControl {
		s.NotFound(w, r)
		return
	}
	ctx := r.Context()
	data := map[string]any{"Layout": "member-search", "SessionURL": sessionURL(r)}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		s.Render(w, r, "net_session.html", data)
		return
	}
	found, err := s.Store.SearchOrgMembers(ctx, c.org.ID, q, memberSearchLimit)
	if err != nil {
		s.ServerError(w, r, "Couldn't search members.", err)
		return
	}
	ids := make([]int64, len(found))
	for i, m := range found {
		ids[i] = m.UserID
	}
	rows, err := s.Store.NetSessionRosterFor(ctx, c.org.ID, c.session, ids)
	if err != nil {
		s.ServerError(w, r, "Couldn't load the roster.", err)
		return
	}
	byID := make(map[int64]*store.RosterEntry, len(rows))
	for i := range rows {
		byID[rows[i].MemberID] = &rows[i]
	}
	matches := make([]memberMatch, len(found))
	for i, m := range found {
		matches[i] = memberMatch{OrgMemberInfo: m, Entry: byID[m.UserID]}
	}
	data["Query"] = q
	data["Matches"] = matches
	s.Render(w, r, "net_session.html", data)
}
