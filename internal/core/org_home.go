package core

import (
	"net/http"
	"slices"
	"time"

	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// Org Home, for members: a banner for each net in session, the org's About and
// links, how to set up a repeater, and a sidebar with counts, the next nets and
// recent activity.

const (
	// homeUpcomingNets caps the "Next nets" card.
	homeUpcomingNets = 3
	// homeActivity caps the "Recent activity" card.
	homeActivity = 8
)

// homeLiveNet is a net whose session is in progress, for Home's banner.
type homeLiveNet struct {
	Net       store.Net
	Session   store.NetSession
	CheckedIn int
}

// homeNets splits an org's nets into those in session now and the next few
// to start, soonest first. nets is bounded by store.MaxNetsPerOrg.
func homeNets(nets []store.NetSummary, now time.Time) (live []homeLiveNet, upcoming []store.NetSummary) {
	for _, n := range nets {
		switch {
		case n.Next == nil:
		case !n.Next.StartsAt.After(now):
			live = append(live, homeLiveNet{Net: n.Net, Session: *n.Next})
		default:
			upcoming = append(upcoming, n)
		}
	}
	slices.SortStableFunc(upcoming, func(a, b store.NetSummary) int { return a.Next.StartsAt.Compare(b.Next.StartsAt) })
	if len(upcoming) > homeUpcomingNets {
		upcoming = upcoming[:homeUpcomingNets]
	}
	return live, upcoming
}

// renderOrgHome renders Home for a member of org.
func (s *Handlers) renderOrgHome(w http.ResponseWriter, r *http.Request, org *store.Org, isAdmin bool) {
	ctx := r.Context()
	now := time.Now()
	fail := func(err error) { s.ServerError(w, r, "could not load organization", err) }

	memberCount, repeaterCount, err := s.Store.OrgCounts(ctx, org.ID)
	if err != nil {
		fail(err)
		return
	}
	links, err := s.Store.ListOrgLinks(ctx, org.ID)
	if err != nil {
		fail(err)
		return
	}
	nets, err := s.Store.ListOrgNetSummaries(ctx, org.ID, now)
	if err != nil {
		fail(err)
		return
	}
	live, upcoming := homeNets(nets, now)
	ids := make([]int64, len(live))
	for i := range live {
		ids[i] = live[i].Session.ID
	}
	counts, err := s.Store.NetSessionsCheckedIn(ctx, org.ID, ids)
	if err != nil {
		fail(err)
		return
	}
	for i := range live {
		live[i].CheckedIn = counts[live[i].Session.ID]
	}
	activity, err := s.Store.ListOrgActivity(ctx, org.ID, now, homeActivity)
	if err != nil {
		fail(err)
		return
	}
	radio, err := s.Store.OrgRadio(ctx, org.ID)
	if err != nil {
		fail(err)
		return
	}
	config, err := s.Store.GetOrgConfigSummary(ctx, org.ID)
	if err != nil {
		fail(err)
		return
	}
	reps, err := web.BuildRepeatersView(ctx, s.Store, org.ID, true)
	if err != nil {
		fail(err)
		return
	}
	data := map[string]any{
		"Org":           org,
		"Nav":           s.orgNav(ctx, org, "home", isAdmin),
		"IsAdmin":       isAdmin,
		"Links":         links,
		"MemberCount":   memberCount,
		"RepeaterCount": repeaterCount,
		"LiveNets":      live,
		"UpcomingNets":  upcoming,
		"Activity":      activity,
		"Radio":         radio,
		"Config":        config,
		"MapPoints":     reps.MapPoints,
	}
	if radio != nil {
		data["Preset"] = presetName(*radio)
	}
	if isAdmin {
		// Observer health is an admin's concern: they register and run them.
		observers, err := s.Store.ListOrgObservers(ctx, org.ID, store.ObserversApproved)
		if err != nil {
			fail(err)
			return
		}
		online := 0
		for _, o := range observers {
			if o.OnlineAt(now) {
				online++
			}
		}
		data["Observers"], data["ObserversOnline"] = len(observers), online
	}
	s.Render(w, r, "org.html", data)
}
