package core

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jleight/meshtender/internal/mesh"
	"github.com/jleight/meshtender/internal/web"
	"github.com/jleight/meshtender/internal/wsbridge"
)

// Packet send tuning for the console's login/command exchanges. perTryReply is
// how long we wait for a reply before resending (a var so tests can shorten it);
// maxSendTries is the maximum number of sends per request.
var perTryReply = 10 * time.Second

const maxSendTries = 4

// applyUserPath seeds the exchanger with a caller-supplied route (the optional
// ?path= query param from the console page) so the login and commands route
// directly with flood fallback. A malformed path is reported and ignored
// (we fall back to flood) rather than failing the session. It returns whether a
// path was set, so the caller can report whether that path actually worked.
func applyUserPath(ex *mesh.Exchanger, r *http.Request, bridge *wsbridge.Conn) bool {
	raw := r.URL.Query().Get("path")
	if raw == "" {
		return false
	}
	path, pathLen, err := mesh.ParsePath(raw)
	if err != nil {
		_ = bridge.Status("warning", "Ignoring the path you entered ("+err.Error()+") — using flood.")
		return false
	}
	if path == nil {
		return false
	}
	ex.SetPath(path, pathLen)
	_ = bridge.Status("info", "Using the path you specified (direct routing, with flood fallback).")
	return true
}

// reportPathOutcome logs whether the login reached the repeater over the
// user-supplied path (a direct RESPONSE reply) or had to fall back to flood (a
// PATH return reply). Only meaningful when a path was set and login succeeded.
func reportPathOutcome(bridge *wsbridge.Conn, lr *mesh.LoginResponse) {
	if lr.FromPath {
		_ = bridge.Status("warning", "The path you specified didn't get through — reached the repeater by flood instead.")
	} else {
		_ = bridge.Status("info", "Reached the repeater directly over the path you specified. ✓")
	}
}

// fetchAndStoreLocation queries the connected repeater for its coordinates
// ("get lat"/"get lon", each retried) and persists them. It reports progress via
// bridge.Status and returns the stored coordinates (ok=false if either read
// failed). Driven by the console's "Fetch location" (getloc) request. Requires
// admin access (guests can't run the get commands) — callers must gate on that
// first.
func (s *Handlers) fetchAndStoreLocation(ctx context.Context, r *http.Request, ex *mesh.Exchanger, bridge *wsbridge.Conn, id int64, debug bool) (lat, lon float64, ok bool) {
	fetchCoord := func(label, cmd string, accept func(text string) bool) (float64, bool) {
		reply, err := ex.CommandAccept(ctx, cmd, accept, func(attempt, max int) {
			if attempt == 1 {
				_ = bridge.Status("info", "Fetching "+label+"…")
			} else {
				_ = bridge.Status("info", fmt.Sprintf("Fetching %s — retry %d/%d…", label, attempt, max))
			}
		})
		if err != nil {
			return 0, false
		}
		return parseLocationFloat(reply)
	}
	lat, okLat := fetchCoord("latitude", "get lat", nil)
	// A slow latitude fetch is retried, which makes the repeater re-run "get lat"
	// and emit duplicate replies; one can straggle in during the "get lon" wait and
	// be misread as the longitude (storing lat,lat). Since the two coordinates
	// differ, reject a longitude reply whose value equals the latitude we just read
	// and keep waiting for the genuine reply.
	lon, okLon := fetchCoord("longitude", "get lon", func(text string) bool {
		f, ok := parseLocationFloat(text)
		if ok && okLat && f == lat {
			if debug {
				_ = bridge.Status("debug", "ignored a stale 'get lat' reply while awaiting longitude")
			}
			return false
		}
		return true
	})
	if !okLat || !okLon {
		_ = bridge.Status("warning", "Could not read a location from the repeater.")
		return 0, 0, false
	}
	if err := s.Store.SetRepeaterLocation(ctx, id, lat, lon); err != nil {
		web.LogError(r, "console: store location", err, "repeater_id", id)
		_ = bridge.Status("error", "Could not store the location — please try again.")
		return 0, 0, false
	}
	_ = bridge.Status("info", fmt.Sprintf("Stored location: %.5f, %.5f", lat, lon))
	return lat, lon, true
}

// parseLocationFloat parses a "get lat"/"get lon" reply like "> 37.7749".
func parseLocationFloat(reply string) (float64, bool) {
	s := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(reply), ">"))
	if i := strings.IndexAny(s, " \t\r\n"); i >= 0 {
		s = s[:i]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
