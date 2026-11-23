package core

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/MeshTender/MeshTender/internal/store"
)

// Nodes is one list of everything a member looks after: the companions they
// carry and the repeaters they own or were given access to. "Node" is
// MeshCore's word for any device on the mesh. Most members have only
// companions, so the page stays as plain as the old companion list until they
// have a repeater.

// Node kinds, as the list's rows and its Show filter name them.
const (
	nodeCompanion = "companion"
	nodeRepeater  = "repeater"
	// nodeShared is a Show choice, not a kind: repeaters shared with you.
	nodeShared = "shared"
)

// nodeRow is one row of the Nodes list: exactly one of Companion or Repeater.
type nodeRow struct {
	Kind      string
	Name      string
	Companion *store.Companion
	Repeater  *store.Repeater
}

// nodeRows merges companions and repeaters into one list sorted by name
// (case-insensitively; companions first on a tie). Both inputs are small and
// bounded — store.MaxCompanionsPerUser companions, and the repeaters one person
// looks after — so this is a sort in memory, not a query.
func nodeRows(companions []*store.Companion, repeaters []*store.Repeater) []nodeRow {
	rows := make([]nodeRow, 0, len(companions)+len(repeaters))
	for _, c := range companions {
		rows = append(rows, nodeRow{Kind: nodeCompanion, Name: c.Name, Companion: c})
	}
	for _, r := range repeaters {
		rows = append(rows, nodeRow{Kind: nodeRepeater, Name: r.Name, Repeater: r})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := strings.ToLower(rows[i].Name), strings.ToLower(rows[j].Name)
		if a != b {
			return a < b
		}
		return rows[i].Kind < rows[j].Kind
	})
	return rows
}

// parseNodeShow is the Show filter's initial choice from ?show=, so a link can
// open the list already filtered (the dashboard's counts, and the old
// /companions and /repeaters addresses). Anything else means all.
func parseNodeShow(q url.Values) string {
	switch v := q.Get("show"); v {
	case nodeCompanion, nodeRepeater, nodeShared:
		return v
	}
	return ""
}

func (s *Handlers) pageNodes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)
	companions, err := s.Store.ListCompanionsForUser(ctx, uid)
	if err != nil {
		s.ServerError(w, r, "Couldn't load your companions.", err)
		return
	}
	repeaters, err := s.Store.ListRepeatersForUser(ctx, uid)
	if err != nil {
		s.ServerError(w, r, "Couldn't load your repeaters.", err)
		return
	}
	var shareCounts map[int64]store.ShareCounts
	if len(repeaters) > 0 {
		if shareCounts, err = s.Store.RepeaterSharingCounts(ctx, uid); err != nil {
			s.ServerError(w, r, "Couldn't load your repeaters' sharing.", err)
			return
		}
	}
	s.Render(w, r, "nodes.html", map[string]any{
		"Nodes":        nodeRows(companions, repeaters),
		"HasRepeaters": len(repeaters) > 0,
		"ShareCounts":  shareCounts,
		"Show":         parseNodeShow(r.URL.Query()),
	})
}

// pageAddNode asks what kind of node is being added.
func (s *Handlers) pageAddNode(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, "node_new.html", nil)
}

// redirectToNodes sends an old list address to the Nodes list, showing that
// kind.
func redirectToNodes(show string) http.HandlerFunc {
	target := "/nodes?" + url.Values{"show": {show}}.Encode()
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	}
}
