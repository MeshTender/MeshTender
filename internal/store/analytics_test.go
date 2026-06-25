package store

import (
	"testing"
	"time"
)

func TestAnalyticsRollup(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	now := time.Now()

	evs := []AnalyticsEvent{
		{Ts: now, Surface: "root", Host: "h", Path: "/a", Method: "GET", Status: 200, Visitor: "v1"},
		{Ts: now, Surface: "root", Host: "h", Path: "/a", Method: "GET", Status: 200, Visitor: "v1"},
		{Ts: now, Surface: "app", Host: "h", Path: "/b", Method: "GET", Status: 200, Visitor: "v2"},
	}
	if err := st.InsertAnalyticsEvents(ctx, evs); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := st.RollupAnalytics(ctx); err != nil {
		t.Fatalf("rollup: %v", err)
	}

	daily, err := st.AnalyticsDaily(ctx, 2)
	if err != nil {
		t.Fatalf("daily: %v", err)
	}
	if len(daily) != 1 || daily[0].Requests != 3 || daily[0].Visitors != 2 {
		t.Fatalf("daily = %+v, want one day with 3 requests / 2 visitors", daily)
	}

	surf, err := st.AnalyticsBySurface(ctx, 2)
	if err != nil {
		t.Fatalf("surface: %v", err)
	}
	bySurface := map[string]int64{}
	for _, s := range surf {
		bySurface[s.Surface] = s.Requests
	}
	if bySurface["root"] != 2 || bySurface["app"] != 1 {
		t.Fatalf("by surface = %v, want root=2 app=1", bySurface)
	}

	paths, err := st.AnalyticsTopPaths(ctx, 2, 10)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	if len(paths) != 2 || paths[0].Path != "/a" || paths[0].Hits != 2 {
		t.Fatalf("top paths = %+v, want /a=2 first", paths)
	}

	// Rollup is idempotent — running again doesn't double-count.
	if err := st.RollupAnalytics(ctx); err != nil {
		t.Fatalf("rollup again: %v", err)
	}
	daily, _ = st.AnalyticsDaily(ctx, 2)
	if len(daily) != 1 || daily[0].Requests != 3 {
		t.Fatalf("after re-rollup daily = %+v, want unchanged", daily)
	}
}

func TestAnalyticsHostsAndVisitors(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	now := time.Now()

	evs := []AnalyticsEvent{
		{Ts: now.Add(-time.Minute), Surface: "custom", Host: "1.2.3.4", Path: "/", Method: "GET", Status: 200, Visitor: "bot"},
		{Ts: now, Surface: "custom", Host: "1.2.3.4", Path: "/robots.txt", Method: "GET", Status: 200, Visitor: "bot"},
		{Ts: now, Surface: "app", Host: "app.x", Path: "/dash", Method: "GET", Status: 200, Visitor: "alice"},
	}
	if err := st.InsertAnalyticsEvents(ctx, evs); err != nil {
		t.Fatalf("insert: %v", err)
	}

	hosts, err := st.AnalyticsTopHosts(ctx, 2, 10)
	if err != nil {
		t.Fatalf("hosts: %v", err)
	}
	if len(hosts) != 2 || hosts[0].Host != "1.2.3.4" || hosts[0].Requests != 2 {
		t.Fatalf("top hosts = %+v, want 1.2.3.4=2 first", hosts)
	}

	vis, err := st.AnalyticsTopVisitors(ctx, 2, 10)
	if err != nil {
		t.Fatalf("visitors: %v", err)
	}
	if len(vis) != 2 || vis[0].Visitor != "bot" || vis[0].Requests != 2 {
		t.Fatalf("top visitors = %+v, want bot=2 first", vis)
	}
	if vis[0].Surface != "custom" || vis[0].LastPath != "/robots.txt" {
		t.Fatalf("bot visitor surface/last-path = %q/%q, want custom//robots.txt", vis[0].Surface, vis[0].LastPath)
	}
}
