package core

import (
	"net/http"
	"time"
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

	daily, err := s.Store.AnalyticsDaily(r.Context(), days)
	if err != nil {
		http.Error(w, "could not load analytics", http.StatusInternalServerError)
		return
	}
	surfaces, err := s.Store.AnalyticsBySurface(r.Context(), days)
	if err != nil {
		http.Error(w, "could not load analytics", http.StatusInternalServerError)
		return
	}
	paths, err := s.Store.AnalyticsTopPaths(r.Context(), days, 20)
	if err != nil {
		http.Error(w, "could not load analytics", http.StatusInternalServerError)
		return
	}
	hosts, err := s.Store.AnalyticsTopHosts(r.Context(), days, 15)
	if err != nil {
		http.Error(w, "could not load analytics", http.StatusInternalServerError)
		return
	}
	visitors, err := s.Store.AnalyticsTopVisitors(r.Context(), days, 15)
	if err != nil {
		http.Error(w, "could not load analytics", http.StatusInternalServerError)
		return
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

	var maxHits int64
	for _, p := range paths {
		if p.Hits > maxHits {
			maxHits = p.Hits
		}
	}
	pathRows := make([]analyticsRow, 0, len(paths))
	for _, p := range paths {
		pathRows = append(pathRows, analyticsRow{Label: p.Path, Value: p.Hits, W: barPct(p.Hits, maxHits)})
	}

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
			LastSeen: v.LastSeen.Format("Jan 2 15:04"),
			Requests: v.Requests,
			W:        barPct(v.Requests, maxVisReq),
		})
	}

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
		"HasData":  len(daily) > 0,
	})
}

// visitorRow is one (daily-rotating) visitor hash for the "traffic by user" table.
type visitorRow struct {
	Visitor  string
	Surface  string
	LastPath string
	LastSeen string
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
