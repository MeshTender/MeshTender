package core

import (
	"errors"
	"net/http"
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
