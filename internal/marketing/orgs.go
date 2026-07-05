package marketing

import (
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
	query := web.Clip(strings.TrimSpace(q.Get("q")), 100)

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
		s.ServerError(w, r, "could not load organizations", err)
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
	// htmx "load more": return just the rows + next control to append in place.
	if r.Header.Get("HX-Request") != "" {
		data["Layout"] = "orgs-frag"
	}
	s.Render(w, r, "orgs.html", data)
}

// renderOrgPublic renders the public-facing org page (name, description, admins,
// counts, and a map of repeaters opted into public display).
func (s *Handlers) renderOrgPublic(w http.ResponseWriter, r *http.Request, org *store.Org, isMember, isAdmin bool) {
	admins, err := s.Store.ListOrgAdmins(r.Context(), org.ID)
	if err != nil {
		s.ServerError(w, r, "could not load organization", err)
		return
	}
	memberCount, repeaterCount, err := s.Store.OrgCounts(r.Context(), org.ID)
	if err != nil {
		s.ServerError(w, r, "could not load organization", err)
		return
	}
	// The map points are loaded lazily by the browser from the cached JSON
	// endpoint (MapURL), so this anonymous page only needs to know whether a map
	// is worth showing — a cheap existence check instead of pulling the full,
	// unbounded point set into every render.
	hasMap, err := s.Store.HasPublicRepeaterPoints(r.Context(), org.ID)
	if err != nil {
		s.ServerError(w, r, "could not load organization", err)
		return
	}
	links, err := s.Store.ListOrgLinks(r.Context(), org.ID)
	if err != nil {
		s.ServerError(w, r, "could not load organization", err)
		return
	}
	uid := s.Auth.CurrentUserID(r.Context())
	s.Render(w, r, "org_public.html", map[string]any{
		"Org": org,
		// The public view never exposes the Members tab — membership isn't public,
		// only the admin list is — so build the nav as a non-member regardless of who
		// is viewing (the tab also 404s on the root host, which has no members route).
		"Nav":           s.OrgNavFor(r.Context(), org.ID, org.Slug, "home", false, isAdmin),
		"Admins":        admins,
		"MemberCount":   memberCount,
		"RepeaterCount": repeaterCount,
		"Links":         links,
		"HasMap":        hasMap,
		"MapURL":        orgMapURL(org.Slug),
		"IsMember":      isMember,
		"LoggedIn":      uid != 0,
		"CanJoin":       uid != 0 && !isMember,
	})
}

// orgMapURL is the cached JSON endpoint the public org pages fetch their map
// points from. Setting MapURL in the render data switches the shared map
// templates from inlining points to fetching them (see meshmap.js).
func orgMapURL(slug string) string {
	return "/orgs/" + slug + "/repeaters.json"
}

// orgRepeatersJSON serves an org's public map points as cached JSON, fetched by
// the public org and repeaters pages. Anonymous and public by design, so it's
// cacheable with a content ETag and a short max-age.
func (s *Handlers) orgRepeatersJSON(w http.ResponseWriter, r *http.Request) {
	id, ok := s.orgID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	points, err := s.Store.ListPublicRepeaterPoints(r.Context(), id)
	if err != nil {
		s.ServerError(w, r, "could not load repeaters", err)
		return
	}
	// Never null: an org with no located public repeaters returns [] so the client
	// always gets a JSON array.
	if points == nil {
		points = []store.MapPoint{}
	}
	if err := web.ServeJSONCached(w, r, time.Minute, points); err != nil {
		s.ServerError(w, r, "could not load repeaters", err)
	}
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
	data := map[string]any{"Org": org, "Nav": s.OrgNavFor(r.Context(), org.ID, org.Slug, "config", false, false), "CanEdit": false}
	var latP, lonP *float64
	if lat, lon, ok := web.PreviewLatLon(r); ok {
		latP, lonP = &lat, &lon
		data["PreviewLat"], data["PreviewLon"] = lat, lon
	}
	cv, err := web.BuildConfigView(r.Context(), s.Store, id, r.URL.Query().Get("profile"), latP, lonP)
	if err != nil {
		s.ServerError(w, r, "could not load profile", err)
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
	afterName, afterID := decodeRepCursor(r.URL.Query().Get("cursor"))
	reps, hasMore, err := s.Store.ListPublicRepeatersPage(r.Context(), id, afterName, afterID)
	if err != nil {
		s.ServerError(w, r, "could not load repeaters", err)
		return
	}
	// The map points come from the cached JSON endpoint (MapURL) client-side, so
	// this page needs only a cheap existence check to decide whether to render the
	// map — not the full point set inlined into the HTML.
	hasMap, err := s.Store.HasPublicRepeaterPoints(r.Context(), id)
	if err != nil {
		s.ServerError(w, r, "could not load repeaters", err)
		return
	}
	rv := web.RepeatersView{Repeaters: reps, HasMap: hasMap, Full: false}
	if hasMore && len(reps) > 0 {
		last := reps[len(reps)-1]
		rv.NextCursor = encodeRepCursor(last.Name, last.RepeaterID)
	}
	data := map[string]any{
		"Org":    org,
		"Nav":    s.OrgNavFor(r.Context(), org.ID, org.Slug, "repeaters", false, false),
		"Reps":   rv,
		"MapURL": orgMapURL(org.Slug),
	}
	// htmx "show more": return just the rows + next control to append in place.
	if r.Header.Get("HX-Request") != "" {
		data["Layout"] = "rep-frag"
	}
	s.Render(w, r, "org_repeaters.html", data)
}

// repCursor is an opaque keyset position in an org's public repeater list: the
// name and id of the last row on the current page.
type repCursor struct {
	Name string `json:"n"`
	ID   int64  `json:"i"`
}

func encodeRepCursor(name string, id int64) string {
	return web.EncodeCursor(repCursor{Name: name, ID: id})
}

// decodeRepCursor reverses encodeRepCursor; a missing or malformed cursor decodes
// to the first page ("", 0).
func decodeRepCursor(tok string) (name string, id int64) {
	c, _ := web.DecodeCursor[repCursor](tok)
	return c.Name, c.ID
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
	return web.EncodeCursor(c)
}

// decodeOrgCursor reverses encodeOrgCursor. A missing or malformed cursor decodes
// to ok=false, i.e. the first page.
func decodeOrgCursor(tok string) (orgCursor, bool) {
	return web.DecodeCursor[orgCursor](tok)
}
