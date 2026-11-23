package core

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/MeshTender/MeshTender/internal/store"
)

// Nodes is one list of everything a member looks after: the companions they
// carry, the repeaters they own or were given access to, and the observers
// they've added. "Node" is
// MeshCore's word for any device on the mesh. Most members have only
// companions, so the page stays as plain as the old companion list until they
// have a repeater.

// Node kinds, as the list's rows and its Show filter name them.
const (
	nodeCompanion = "companion"
	nodeRepeater  = "repeater"
	nodeObserver  = "observer"
	// nodeShared is a Show choice, not a kind: repeaters shared with you.
	nodeShared = "shared"
)

// nodeRow is one row of the Nodes list: a companion, a repeater, or an
// observer. A repeater the user has also added as an observer is one row with
// both Repeater and Observer set.
type nodeRow struct {
	Kind      string
	Name      string // "" for an observer no organization has heard from yet
	Key       string
	Companion *store.Companion
	Repeater  *store.Repeater
	Observer  *store.UserObserver
}

// nodeRows merges companions, repeaters and observers into one list sorted by
// name (case-insensitively; by kind on a tie). An observer with no name yet
// sorts after the named rows, by key. Every input is small and bounded —
// store.MaxCompanionsPerUser companions, store.MaxObserversPerUser observers,
// and the repeaters one person looks after — so this is a sort in memory, not
// a query.
func nodeRows(companions []*store.Companion, repeaters []*store.Repeater, observers []*store.UserObserver) []nodeRow {
	byKey := make(map[string]*store.UserObserver, len(observers))
	for _, o := range observers {
		byKey[o.PublicKeyHex] = o
	}
	rows := make([]nodeRow, 0, len(companions)+len(repeaters)+len(observers))
	for _, c := range companions {
		rows = append(rows, nodeRow{Kind: nodeCompanion, Name: c.Name, Key: c.PublicKeyHex, Companion: c})
	}
	for _, r := range repeaters {
		o := byKey[r.PublicKeyHex]
		delete(byKey, r.PublicKeyHex) // shown on the repeater's row
		rows = append(rows, nodeRow{Kind: nodeRepeater, Name: r.Name, Key: r.PublicKeyHex, Repeater: r, Observer: o})
	}
	for _, o := range observers {
		if byKey[o.PublicKeyHex] != nil {
			rows = append(rows, nodeRow{Kind: nodeObserver, Name: o.Name, Key: o.PublicKeyHex, Observer: o})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := strings.ToLower(rows[i].Name), strings.ToLower(rows[j].Name)
		if (a == "") != (b == "") {
			return a != ""
		}
		if a != b {
			return a < b
		}
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].Key < rows[j].Key
	})
	return rows
}

// parseNodeShow is the Show filter's initial choice from ?show=, so a link can
// open the list already filtered (the dashboard's counts, and the old
// /companions and /repeaters addresses). Anything else means all.
func parseNodeShow(q url.Values) string {
	switch v := q.Get("show"); v {
	case nodeCompanion, nodeRepeater, nodeObserver, nodeShared:
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
	observers, err := s.Store.ListObserversForUser(ctx, uid)
	if err != nil {
		s.ServerError(w, r, "Couldn't load your observers.", err)
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
		"Nodes": nodeRows(companions, repeaters, observers),
		// Filters once there's more than companions to sort through.
		"HasFilters":  len(repeaters)+len(observers) > 0,
		"ShareCounts": shareCounts,
		"Show":        parseNodeShow(r.URL.Query()),
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
