package core

import (
	"net/http"
	"time"

	"github.com/jleight/meshtender/internal/analytics"
	"github.com/jleight/meshtender/internal/store"
)

// analyticsBar is one day's column in the traffic charts; heights are percentages
// of the busiest day so the template just sets a CSS height.
type analyticsBar struct {
	Label    string
	Requests int64
	Visitors int64
	ReqH     int
	VisH     int
}

// analyticsRow is a labeled value with a bar width (percent of the max), used for
// the by-surface and top-paths tables.
type analyticsRow struct {
	Label string
	Value int64
	W     int
}

// pageAnalytics renders the traffic dashboard from the rolled-up aggregate tables.
func (s *Handlers) pageAnalytics(w http.ResponseWriter, r *http.Request) {
	days := 30
	switch r.URL.Query().Get("days") {
	case "7":
		days = 7
	case "90":
		days = 90
	}

	// Everything on the main dashboard reads the "visit" kind only. Scanners and
	// crawlers are recorded too (see internal/analytics classify), but they'd
	// dwarf the real numbers here — they get their own cards below.
	daily, err := s.Store.AnalyticsDaily(r.Context(), days, analytics.KindVisit)
	if err != nil {
		s.ServerError(w, r, "could not load analytics", err)
		return
	}
	surfaces, err := s.Store.AnalyticsBySurface(r.Context(), days, analytics.KindVisit)
	if err != nil {
		s.ServerError(w, r, "could not load analytics", err)
		return
	}
	paths, err := s.Store.AnalyticsTopPaths(r.Context(), days, analytics.KindVisit, 20)
	if err != nil {
		s.ServerError(w, r, "could not load analytics", err)
		return
	}
	hosts, err := s.Store.AnalyticsTopHosts(r.Context(), days, analytics.KindVisit, 15)
	if err != nil {
		s.ServerError(w, r, "could not load analytics", err)
		return
	}
	visitors, err := s.Store.AnalyticsTopVisitors(r.Context(), days, analytics.KindVisit, 15)
	if err != nil {
		s.ServerError(w, r, "could not load analytics", err)
		return
	}
	kinds, err := s.Store.AnalyticsKindSummary(r.Context(), days)
	if err != nil {
		s.ServerError(w, r, "could not load analytics", err)
		return
	}
	probePaths, err := s.Store.AnalyticsTopPaths(r.Context(), days, analytics.KindProbe, 10)
	if err != nil {
		s.ServerError(w, r, "could not load analytics", err)
		return
	}
	botPaths, err := s.Store.AnalyticsTopPaths(r.Context(), days, analytics.KindBot, 10)
	if err != nil {
		s.ServerError(w, r, "could not load analytics", err)
		return
	}
	byKind := make(map[string]store.KindStat, len(kinds))
	for _, k := range kinds {
		byKind[k.Kind] = k
	}

	var maxReq, maxVis, totalReq int64
	for _, d := range daily {
		totalReq += d.Requests
		if d.Requests > maxReq {
			maxReq = d.Requests
		}
		if d.Visitors > maxVis {
			maxVis = d.Visitors
		}
	}
	// The daily chart buckets are aggregated per UTC calendar day (see the store
	// query), so their axis labels and the "is today" check below stay in UTC —
	// unlike discrete instants (LastSeen), these are day buckets, not moments to
	// localize per viewer.
	bars := make([]analyticsBar, 0, len(daily))
	for _, d := range daily {
		bars = append(bars, analyticsBar{
			Label:    d.Day.Format("1/2"),
			Requests: d.Requests,
			Visitors: d.Visitors,
			ReqH:     barPct(d.Requests, maxReq),
			VisH:     barPct(d.Visitors, maxVis),
		})
	}

	// "Today" only if the latest rolled-up day is actually today (UTC).
	var todayReq, todayVis int64
	if n := len(daily); n > 0 && daily[n-1].Day.UTC().Format("2006-01-02") == time.Now().UTC().Format("2006-01-02") {
		todayReq, todayVis = daily[n-1].Requests, daily[n-1].Visitors
	}

	var maxSurface int64
	for _, x := range surfaces {
		if x.Requests > maxSurface {
			maxSurface = x.Requests
		}
	}
	surfaceRows := make([]analyticsRow, 0, len(surfaces))
	for _, x := range surfaces {
		surfaceRows = append(surfaceRows, analyticsRow{Label: x.Surface, Value: x.Requests, W: barPct(x.Requests, maxSurface)})
	}

	pathRows := toPathRows(paths)

	var maxHostReq int64
	for _, h := range hosts {
		if h.Requests > maxHostReq {
			maxHostReq = h.Requests
		}
	}
	hostRows := make([]analyticsRow, 0, len(hosts))
	for _, h := range hosts {
		label := h.Host
		if label == "" {
			label = "(none)"
		}
		hostRows = append(hostRows, analyticsRow{Label: label, Value: h.Requests, W: barPct(h.Requests, maxHostReq)})
	}

	var maxVisReq int64
	for _, v := range visitors {
		if v.Requests > maxVisReq {
			maxVisReq = v.Requests
		}
	}
	visitorRows := make([]visitorRow, 0, len(visitors))
	for _, v := range visitors {
		visitorRows = append(visitorRows, visitorRow{
			Visitor:  v.Visitor,
			Surface:  v.Surface,
			LastPath: v.LastPath,
			LastSeen: v.LastSeen,
			Requests: v.Requests,
			W:        barPct(v.Requests, maxVisReq),
		})
	}

	probes, bots := byKind[analytics.KindProbe], byKind[analytics.KindBot]
	s.Render(w, r, "analytics.html", map[string]any{
		"Days":     days,
		"Bars":     bars,
		"Surfaces": surfaceRows,
		"Paths":    pathRows,
		"Hosts":    hostRows,
		"Visitors": visitorRows,
		"TotalReq": totalReq,
		"TodayReq": todayReq,
		"TodayVis": todayVis,
		// Scanner and crawler traffic, held apart from the figures above so a
		// wordlist replay can't read as an audience.
		"ProbeReq":     probes.Requests,
		"ProbeSources": probes.Sources,
		"ProbePaths":   toPathRows(probePaths),
		"BotReq":       bots.Requests,
		"BotSources":   bots.Sources,
		"BotPaths":     toPathRows(botPaths),
		// 404s with no attack signature — broken links worth fixing. Filtered out
		// of every figure above, so without this they'd be invisible entirely.
		"NotFoundReq": byKind[analytics.KindNotFound].Requests,
		"HasData":     len(daily) > 0 || len(kinds) > 0,
	})
}

// toPathRows scales a set of path counts into labeled bars.
func toPathRows(paths []store.PathStat) []analyticsRow {
	var max int64
	for _, p := range paths {
		if p.Hits > max {
			max = p.Hits
		}
	}
	rows := make([]analyticsRow, 0, len(paths))
	for _, p := range paths {
		rows = append(rows, analyticsRow{Label: p.Path, Value: p.Hits, W: barPct(p.Hits, max)})
	}
	return rows
}

// visitorRow is one (daily-rotating) visitor hash for the "traffic by user" table.
type visitorRow struct {
	Visitor  string
	Surface  string
	LastPath string
	LastSeen time.Time // rendered client-side (localized) via the `ts` template func
	Requests int64
	W        int
}

// barPct scales v to a 0–100 height/width, with a small floor so non-zero values
// stay visible.
func barPct(v, max int64) int {
	if max <= 0 || v <= 0 {
		return 0
	}
	p := int(v * 100 / max)
	if p < 2 {
		p = 2
	}
	return p
}
