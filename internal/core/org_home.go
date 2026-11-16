package core

import (
	"net/http"
	"time"

	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// Org Home, for members: what's happening week to week. A banner for each net
// in session, a nudge while the member has no registered companion, the
// upcoming nets and recent activity. What doesn't change week to week lives
// elsewhere: About on the public page, the radio and configuration in the Guide,
// repeaters and members on their tabs.

// homeActivity caps the "Recent activity" card.
const homeActivity = 8

// homeLiveNet is a net whose session is in progress, for Home's banner.
type homeLiveNet struct {
	Net       store.Net
	Session   store.NetSession
	CheckedIn int
}

// homeNets splits an org's scheduled nets (web.ScheduledNets: active, soonest
// first) into those in session now and those still to start.
func homeNets(scheduled []store.NetSummary, now time.Time) (live []homeLiveNet, upcoming []store.NetSummary) {
	for _, n := range scheduled {
		if n.Next.StartsAt.After(now) {
			upcoming = append(upcoming, n)
		} else {
			live = append(live, homeLiveNet{Net: n.Net, Session: *n.Next})
		}
	}
	return live, upcoming
}

// renderOrgHome renders Home for a member of org.
func (s *Handlers) renderOrgHome(w http.ResponseWriter, r *http.Request, org *store.Org, isAdmin bool) {
	ctx := r.Context()
	now := time.Now()
	fail := func(err error) { s.ServerError(w, r, "could not load organization", err) }

	nets, err := s.Store.ListOrgNetSummaries(ctx, org.ID, now)
	if err != nil {
		fail(err)
		return
	}
	live, upcoming := homeNets(web.ScheduledNets(nets), now)
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
	// Bounded by store.MaxCompanionsPerUser.
	companions, err := s.Store.ListCompanionsForUser(ctx, s.Auth.CurrentUserID(ctx))
	if err != nil {
		fail(err)
		return
	}
	s.Render(w, r, "org.html", map[string]any{
		"Org":          org,
		"Nav":          s.orgNav(ctx, org, "home", isAdmin),
		"IsAdmin":      isAdmin,
		"Now":          now,
		"LiveNets":     live,
		"UpcomingNets": upcoming,
		"Activity":     activity,
		"HasCompanion": len(companions) > 0,
	})
}
