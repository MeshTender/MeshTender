package marketing

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// pageOrgs is the public organization directory. It renders anonymously — the
// root host carries no session — so every visitor sees the same list. The
// directory is keyset-paginated via an opaque ?cursor token.
func (s *Handlers) pageOrgs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	sortKey := store.NormalizeOrgSort(q.Get("sort"))
	query := strings.TrimSpace(q.Get("q"))
	if len(query) > 100 {
		query = query[:100]
	}

	var p store.OrgListParams
	// A cursor carries the authoritative sort, search, and keyset position for
	// the page it points at, so paging stays consistent without re-sending the
	// form; the sort/q params only matter on the first (cursorless) page.
	if c, ok := decodeOrgCursor(q.Get("cursor")); ok {
		sortKey = store.NormalizeOrgSort(c.Sort)
		query = c.Query
		p.HasCursor = true
		p.AfterName = c.Name
		p.AfterCount = c.Count
		p.AfterID = c.ID
		if c.Time != "" {
			p.AfterTime, _ = time.Parse(time.RFC3339Nano, c.Time)
		}
	}
	p.Sort = sortKey
	p.Query = query

	all, hasMore, err := s.Store.ListPublicOrgsPage(r.Context(), p)
	if err != nil {
		http.Error(w, "could not load orgs", http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"All":   all,
		"Error": q.Get("error"),
		"Sort":  string(sortKey),
		"Query": query,
	}
	if hasMore && len(all) > 0 {
		data["NextCursor"] = encodeOrgCursor(nextOrgCursor(sortKey, query, all[len(all)-1]))
	}
	s.Render(w, r, "orgs.html", data)
}

// renderOrgPublic renders the public-facing org page (name, description, admins,
// counts, and a map of repeaters opted into public display).
func (s *Handlers) renderOrgPublic(w http.ResponseWriter, r *http.Request, org *store.Org, isMember bool) {
	admins, err := s.Store.ListOrgAdminNames(r.Context(), org.ID)
	if err != nil {
		http.Error(w, "could not load org", http.StatusInternalServerError)
		return
	}
	memberCount, repeaterCount, err := s.Store.OrgCounts(r.Context(), org.ID)
	if err != nil {
		http.Error(w, "could not load org", http.StatusInternalServerError)
		return
	}
	pubReps, err := s.Store.ListPublicMapRepeaters(r.Context(), org.ID)
	if err != nil {
		http.Error(w, "could not load org", http.StatusInternalServerError)
		return
	}
	uid := s.Auth.CurrentUserID(r.Context())
	s.Render(w, r, "org_public.html", map[string]any{
		"Org":           org,
		"Nav":           web.OrgNav(org.Slug, "home", isMember),
		"Admins":        admins,
		"MemberCount":   memberCount,
		"RepeaterCount": repeaterCount,
		"Repeaters":     pubReps,
		"HasMap":        len(pubReps) > 0,
		"IsMember":      isMember,
		"LoggedIn":      uid != 0,
		"CanJoin":       uid != 0 && !isMember,
	})
}

// pageOrgConfig renders an org's recommended configuration read-only for anonymous
// visitors (the app host serves the same shared page to signed-in users).
func (s *Handlers) pageOrgConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := s.orgID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	org, err := s.Store.GetOrg(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data := map[string]any{"Org": org, "Nav": web.OrgNav(org.Slug, "config", false), "CanEdit": false}
	var latP, lonP *float64
	if lat, lon, ok := web.PreviewLatLon(r); ok {
		latP, lonP = &lat, &lon
		data["PreviewLat"], data["PreviewLon"] = lat, lon
	}
	cv, err := web.BuildConfigView(r.Context(), s.Store, id, latP, lonP)
	if err != nil {
		http.Error(w, "could not load profile", http.StatusInternalServerError)
		return
	}
	data["Config"] = cv
	s.Render(w, r, "org_config.html", data)
}

// pageOrgRepeaters renders an org's public repeaters (those opted into the public
// map) with a map, for anonymous visitors.
func (s *Handlers) pageOrgRepeaters(w http.ResponseWriter, r *http.Request) {
	id, ok := s.orgID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	org, err := s.Store.GetOrg(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rv, err := web.BuildRepeatersView(r.Context(), s.Store, id, false)
	if err != nil {
		http.Error(w, "could not load repeaters", http.StatusInternalServerError)
		return
	}
	s.Render(w, r, "org_repeaters.html", map[string]any{
		"Org":  org,
		"Nav":  web.OrgNav(org.Slug, "repeaters", false),
		"Reps": rv,
	})
}

// orgCursor is an opaque directory position: the sort and search it belongs to,
// plus the sort key and id of the last org on the current page.
type orgCursor struct {
	Sort  string `json:"s,omitempty"`
	Query string `json:"q,omitempty"`
	Name  string `json:"n,omitempty"`
	Count int    `json:"c,omitempty"`
	Time  string `json:"t,omitempty"` // RFC3339Nano, for the "newest" sort
	ID    int64  `json:"i"`
}

// nextOrgCursor builds the cursor pointing just past last, carrying the current
// sort and search so the next page reproduces them.
func nextOrgCursor(sort store.OrgSort, query string, last store.OrgSummary) orgCursor {
	c := orgCursor{Sort: string(sort), Query: query, ID: last.ID}
	switch sort {
	case store.OrgSortName:
		c.Name = last.Name
	case store.OrgSortRepeaters:
		c.Count = last.RepeaterCount
	case store.OrgSortNewest:
		c.Time = last.CreatedAt.Format(time.RFC3339Nano)
	default: // OrgSortMembers
		c.Count = last.MemberCount
	}
	return c
}

// encodeOrgCursor packs a directory position into an opaque, URL-safe token.
func encodeOrgCursor(c orgCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeOrgCursor reverses encodeOrgCursor. A missing or malformed cursor decodes
// to ok=false, i.e. the first page.
func decodeOrgCursor(tok string) (orgCursor, bool) {
	if tok == "" {
		return orgCursor{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return orgCursor{}, false
	}
	var c orgCursor
	if json.Unmarshal(b, &c) != nil {
		return orgCursor{}, false
	}
	return c, true
}
