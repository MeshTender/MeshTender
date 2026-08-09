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
		{Ts: now, Surface: "root", Host: "h", Path: "/a", Method: "GET", Status: 200, Kind: "visit", Visitor: "v1"},
		{Ts: now, Surface: "root", Host: "h", Path: "/a", Method: "GET", Status: 200, Kind: "visit", Visitor: "v1"},
		{Ts: now, Surface: "app", Host: "h", Path: "/b", Method: "GET", Status: 200, Kind: "visit", Visitor: "v2"},
	}
	if err := st.InsertAnalyticsEvents(ctx, evs); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := st.RollupAnalytics(ctx); err != nil {
		t.Fatalf("rollup: %v", err)
	}

	daily, err := st.AnalyticsDaily(ctx, 2, "visit")
	if err != nil {
		t.Fatalf("daily: %v", err)
	}
	if len(daily) != 1 || daily[0].Requests != 3 || daily[0].Visitors != 2 {
		t.Fatalf("daily = %+v, want one day with 3 requests / 2 visitors", daily)
	}

	surf, err := st.AnalyticsBySurface(ctx, 2, "visit")
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

	paths, err := st.AnalyticsTopPaths(ctx, 2, "visit", 10)
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
	daily, _ = st.AnalyticsDaily(ctx, 2, "visit")
	if len(daily) != 1 || daily[0].Requests != 3 {
		t.Fatalf("after re-rollup daily = %+v, want unchanged", daily)
	}
}

// TestAnalyticsRollupSeparatesKinds is the regression this whole column exists
// for: one scanner replaying a wordlist used to outweigh every real visitor in
// the rollups. Its requests must still be stored and countable, just never mixed
// into the visit figures.
func TestAnalyticsRollupSeparatesKinds(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	now := time.Now()

	evs := []AnalyticsEvent{
		{Ts: now, Surface: "root", Host: "h", Path: "/", Method: "GET", Status: 200, Kind: "visit", Visitor: "alice"},
		{Ts: now, Surface: "root", Host: "h", Path: "/docs", Method: "GET", Status: 200, Kind: "visit", Visitor: "alice"},
		{Ts: now, Surface: "root", Host: "h", Path: "/orgs", Method: "GET", Status: 200, Kind: "bot", Visitor: "googlebot"},
		{Ts: now, Surface: "root", Host: "h", Path: "/gone", Method: "GET", Status: 404, Kind: "notfound", Visitor: "alice"},
	}
	// One scanner, many paths, all in a day — the shape that swamped the charts.
	for i, p := range []string{"/wp-login.php", "/.env", "/index.php", "/firebase-key.json", "/manager/html"} {
		evs = append(evs, AnalyticsEvent{
			Ts: now.Add(-time.Duration(i) * time.Second), Surface: "root", Host: "h",
			Path: p, Method: "GET", Status: 404, Kind: "probe", Visitor: "scanner",
		})
	}
	if err := st.InsertAnalyticsEvents(ctx, evs); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := st.RollupAnalytics(ctx); err != nil {
		t.Fatalf("rollup: %v", err)
	}

	// Visits are unpolluted: 2 requests from 1 visitor, not 9 from 4.
	visits, err := st.AnalyticsDaily(ctx, 2, "visit")
	if err != nil {
		t.Fatalf("daily visits: %v", err)
	}
	if len(visits) != 1 || visits[0].Requests != 2 || visits[0].Visitors != 1 {
		t.Fatalf("visit day = %+v, want 2 requests / 1 visitor", visits)
	}

	// The scanner is still fully accounted for under its own kind.
	probes, err := st.AnalyticsDaily(ctx, 2, "probe")
	if err != nil {
		t.Fatalf("daily probes: %v", err)
	}
	if len(probes) != 1 || probes[0].Requests != 5 || probes[0].Visitors != 1 {
		t.Fatalf("probe day = %+v, want 5 requests / 1 source", probes)
	}

	bots, err := st.AnalyticsDaily(ctx, 2, "bot")
	if err != nil {
		t.Fatalf("daily bots: %v", err)
	}
	if len(bots) != 1 || bots[0].Requests != 1 {
		t.Fatalf("bot day = %+v, want 1 request", bots)
	}

	// Per-kind path and visitor views read only their own kind.
	probePaths, err := st.AnalyticsTopPaths(ctx, 2, "probe", 10)
	if err != nil {
		t.Fatalf("probe paths: %v", err)
	}
	if len(probePaths) != 5 {
		t.Fatalf("probe paths = %+v, want the 5 probed paths", probePaths)
	}
	visitPaths, err := st.AnalyticsTopPaths(ctx, 2, "visit", 10)
	if err != nil {
		t.Fatalf("visit paths: %v", err)
	}
	for _, p := range visitPaths {
		if p.Path == "/wp-login.php" {
			t.Fatalf("a probed path leaked into the visit paths: %+v", visitPaths)
		}
	}

	vis, err := st.AnalyticsTopVisitors(ctx, 2, "visit", 10)
	if err != nil {
		t.Fatalf("visitors: %v", err)
	}
	if len(vis) != 1 || vis[0].Visitor != "alice" {
		t.Fatalf("top visitors = %+v, want only alice — the scanner must not rank here", vis)
	}
}

// TestAnalyticsKindSummary: the cards need a true distinct-source count across
// the window. Summing the daily rollups would count a scanner that runs every
// day once per day, turning one attacker into a crowd.
func TestAnalyticsKindSummary(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	now := time.Now()

	evs := []AnalyticsEvent{
		{Ts: now, Surface: "root", Host: "h", Path: "/", Method: "GET", Status: 200, Kind: "visit", Visitor: "alice"},
		{Ts: now, Surface: "root", Host: "h", Path: "/docs", Method: "GET", Status: 200, Kind: "visit", Visitor: "bob"},
		{Ts: now, Surface: "root", Host: "h", Path: "/orgs", Method: "GET", Status: 200, Kind: "bot", Visitor: "googlebot"},
		{Ts: now, Surface: "root", Host: "h", Path: "/gone", Method: "GET", Status: 404, Kind: "notfound", Visitor: "alice"},
	}
	// The same scanner across two days: 4 requests, but one source.
	for i, p := range []string{"/.env", "/wp-login.php", "/.git/config", "/index.php"} {
		evs = append(evs, AnalyticsEvent{
			Ts: now.Add(-time.Duration(i) * 12 * time.Hour), Surface: "root", Host: "h",
			Path: p, Method: "GET", Status: 404, Kind: "probe", Visitor: "scanner",
		})
	}
	if err := st.InsertAnalyticsEvents(ctx, evs); err != nil {
		t.Fatalf("insert: %v", err)
	}

	kinds, err := st.AnalyticsKindSummary(ctx, 7)
	if err != nil {
		t.Fatalf("kind summary: %v", err)
	}
	got := map[string]KindStat{}
	for _, k := range kinds {
		got[k.Kind] = k
	}
	if p := got["probe"]; p.Requests != 4 || p.Sources != 1 {
		t.Errorf("probe = %+v, want 4 requests from 1 source", p)
	}
	if v := got["visit"]; v.Requests != 2 || v.Sources != 2 {
		t.Errorf("visit = %+v, want 2 requests from 2 sources", v)
	}
	if b := got["bot"]; b.Requests != 1 || b.Sources != 1 {
		t.Errorf("bot = %+v, want 1 request from 1 source", b)
	}
	if n := got["notfound"]; n.Requests != 1 {
		t.Errorf("notfound = %+v, want 1 request", n)
	}
}

func TestAnalyticsHostsAndVisitors(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	now := time.Now()

	evs := []AnalyticsEvent{
		{Ts: now.Add(-time.Minute), Surface: "custom", Host: "1.2.3.4", Path: "/", Method: "GET", Status: 200, Kind: "bot", Visitor: "bot"},
		{Ts: now, Surface: "custom", Host: "1.2.3.4", Path: "/robots.txt", Method: "GET", Status: 200, Kind: "bot", Visitor: "bot"},
		{Ts: now, Surface: "app", Host: "app.x", Path: "/dash", Method: "GET", Status: 200, Kind: "visit", Visitor: "alice"},
	}
	if err := st.InsertAnalyticsEvents(ctx, evs); err != nil {
		t.Fatalf("insert: %v", err)
	}

	hosts, err := st.AnalyticsTopHosts(ctx, 2, "bot", 10)
	if err != nil {
		t.Fatalf("hosts: %v", err)
	}
	if len(hosts) != 1 || hosts[0].Host != "1.2.3.4" || hosts[0].Requests != 2 {
		t.Fatalf("top bot hosts = %+v, want 1.2.3.4=2", hosts)
	}

	vis, err := st.AnalyticsTopVisitors(ctx, 2, "bot", 10)
	if err != nil {
		t.Fatalf("visitors: %v", err)
	}
	if len(vis) != 1 || vis[0].Visitor != "bot" || vis[0].Requests != 2 {
		t.Fatalf("top bot visitors = %+v, want bot=2", vis)
	}
	if vis[0].Surface != "custom" || vis[0].LastPath != "/robots.txt" {
		t.Fatalf("bot visitor surface/last-path = %q/%q, want custom//robots.txt", vis[0].Surface, vis[0].LastPath)
	}

	// The human is reachable under her own kind, and only there.
	human, err := st.AnalyticsTopVisitors(ctx, 2, "visit", 10)
	if err != nil {
		t.Fatalf("visit visitors: %v", err)
	}
	if len(human) != 1 || human[0].Visitor != "alice" {
		t.Fatalf("visit visitors = %+v, want only alice", human)
	}
}
