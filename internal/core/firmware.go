package core

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MeshTender/MeshTender/internal/firmware"
	"github.com/MeshTender/MeshTender/internal/web"
)

// manifestMaxAge is how long a browser or proxy may reuse an install manifest.
// The catalog itself refreshes every few minutes, so a minute adds little.
const manifestMaxAge = time.Minute

// firmwareManifest serves the esp-web-tools manifest that installs the latest
// stable observer firmware for one board and role (its PlatformIO env).
func (s *Handlers) firmwareManifest(w http.ResponseWriter, r *http.Request) {
	idx, err := s.firmware.Index(r.Context())
	if err != nil {
		web.LogError(r, "firmware index unavailable", err)
		http.Error(w, "The firmware catalog is unavailable. Try again in a few minutes.", http.StatusServiceUnavailable)
		return
	}
	f, ok := idx.Lookup(firmware.StableChannel, chi.URLParam(r, "env"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, ok := f.LatestBuild()
	if !ok {
		http.NotFound(w, r)
		return
	}
	m, err := firmware.WebToolsManifest(f, b)
	if errors.Is(err, firmware.ErrNotFlashable) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't build the install manifest.", err)
		return
	}
	if err := web.ServeJSONCached(w, r, manifestMaxAge, m); err != nil {
		s.ServerError(w, r, "Couldn't build the install manifest.", err)
	}
}

// installBoard is one board the install page offers for a role.
type installBoard struct {
	Env       string
	Board     string
	Version   string
	Published string // date only, e.g. "November 22, 2026"
}

// installRole is a role (room server, repeater) and the boards it's built for.
type installRole struct {
	Role   string
	Name   string // "Room server"
	Boards []installBoard
}

// roleOrder puts room servers first (most observers are room servers), then
// repeaters, then any role the index adds later, by name.
var roleOrder = map[string]int{"room_server": 0, "repeater": 1}

// installRoles lists what the install page can offer: every stable firmware
// with a latest build esp-web-tools can flash, grouped by role, boards by name.
func installRoles(idx *firmware.Index) []installRole {
	byRole := map[string]*installRole{}
	for _, c := range idx.Channels {
		if c.Name != firmware.StableChannel {
			continue
		}
		for _, f := range c.Firmware {
			b, ok := f.LatestBuild()
			if !ok || f.Role == "" {
				continue
			}
			if _, err := firmware.WebToolsManifest(f, b); err != nil {
				continue
			}
			ir := byRole[f.Role]
			if ir == nil {
				name := firmware.RoleName(f.Role)
				ir = &installRole{Role: f.Role, Name: strings.ToUpper(name[:1]) + name[1:]}
				byRole[f.Role] = ir
			}
			ib := installBoard{Env: f.Env, Board: f.Board, Version: b.Version}
			if !b.PublishedAt.IsZero() {
				ib.Published = b.PublishedAt.UTC().Format("January 2, 2006")
			}
			ir.Boards = append(ir.Boards, ib)
		}
	}
	roles := make([]installRole, 0, len(byRole))
	for _, ir := range byRole {
		sort.Slice(ir.Boards, func(i, j int) bool { return ir.Boards[i].Board < ir.Boards[j].Board })
		roles = append(roles, *ir)
	}
	sort.Slice(roles, func(i, j int) bool {
		oi, iok := roleOrder[roles[i].Role]
		oj, jok := roleOrder[roles[j].Role]
		switch {
		case iok && jok:
			return oi < oj
		case iok != jok:
			return iok
		}
		return roles[i].Name < roles[j].Name
	})
	return roles
}
