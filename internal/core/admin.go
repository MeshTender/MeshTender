package core

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// requireCap is middleware that allows the request only if the current user
// satisfies pick; otherwise it 404s (without revealing the admin area exists).
func (s *Handlers) requireCap(pick func(*store.User) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, err := s.Store.GetUserByID(r.Context(), s.Auth.CurrentUserID(r.Context()))
			if err != nil || !pick(u) {
				s.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func capAny(u *store.User) bool     { return u.CapManageUsers || u.CapManageCatalog }
func capCatalog(u *store.User) bool { return u.CapManageCatalog }
func capUsers(u *store.User) bool   { return u.CapManageUsers }

// categoryGroup is a named list of per-command view models (the name is the
// feature area). The element type varies per page (raw command, share checkbox,
// per-tier checkbox); groupByFeature builds them all.
type categoryGroup[T any] struct {
	Name     string
	Commands []T
}

// groupByFeature buckets the catalog by feature area (store.Command.Feature) —
// the same grouping the consent page uses — ordered by featureOrder, mapping
// each command via mk.
func groupByFeature[T any](catalog []*store.Command, mk func(*store.Command) T) []categoryGroup[T] {
	byFeature := map[string][]T{}
	var present []string
	for _, c := range catalog {
		if _, ok := byFeature[c.Feature]; !ok {
			present = append(present, c.Feature)
		}
		byFeature[c.Feature] = append(byFeature[c.Feature], mk(c))
	}
	orderFeatures(present)
	groups := make([]categoryGroup[T], 0, len(present))
	for _, f := range present {
		groups = append(groups, categoryGroup[T]{Name: f, Commands: byFeature[f]})
	}
	return groups
}

// catalogGroup buckets raw catalog commands for the admin catalog page.
type catalogGroup = categoryGroup[*store.Command]

func groupCatalog(catalog []*store.Command) []catalogGroup {
	return groupByFeature(catalog, func(c *store.Command) *store.Command { return c })
}

func (s *Handlers) pageAdmin(w http.ResponseWriter, r *http.Request) {
	u, err := s.Store.GetUserByID(r.Context(), s.Auth.CurrentUserID(r.Context()))
	if err != nil {
		s.ServerError(w, r, "could not load account", err)
		return
	}
	s.Render(w, r, "admin.html", map[string]any{
		"CanCatalog": u.CapManageCatalog,
		"CanUsers":   u.CapManageUsers,
	})
}

func (s *Handlers) pageCatalog(w http.ResponseWriter, r *http.Request) {
	catalog, err := s.Store.ListCommands(r.Context())
	if err != nil {
		s.ServerError(w, r, "could not load catalog", err)
		return
	}
	s.Render(w, r, "admin_catalog.html", map[string]any{
		"Groups": groupCatalog(catalog),
		"Saved":  r.URL.Query().Get("saved") != "",
	})
}

func (s *Handlers) handleUpdateCommand(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	on := func(name string) bool { return r.FormValue(name) != "" }
	if err := s.Store.UpdateCommandFlags(r.Context(), id,
		on("risky"), on("share"), on("org_member"), on("org_admin")); err != nil {
		s.ServerError(w, r, "could not save", err)
		return
	}
	http.Redirect(w, r, "/admin/catalog?saved=1", http.StatusSeeOther)
}

// userCursor carries the sort, search, capability filter, and keyset position of
// an admin user-list page, so "load more" stays consistent without re-sending
// the form; the sort/q/cap params only matter on the first (cursorless) page.
type userCursor struct {
	Sort  string `json:"s,omitempty"`
	Query string `json:"q,omitempty"`
	Cap   string `json:"c,omitempty"`
	Name  string `json:"n,omitempty"` // username-sort cursor
	Time  string `json:"t,omitempty"` // RFC3339Nano, for last-login / newest sorts
	ID    int64  `json:"i"`
}

func nextUserCursor(p store.UserListParams, last *store.User) userCursor {
	c := userCursor{Sort: string(p.Sort), Query: p.Query, Cap: string(p.Cap), ID: last.ID}
	switch p.Sort {
	case store.UserSortLastLogin:
		c.Time = last.LastLoginKey().Format(time.RFC3339Nano)
	case store.UserSortNewest:
		c.Time = last.CreatedAt.Format(time.RFC3339Nano)
	default: // UserSortName
		c.Name = last.Username
	}
	return c
}

func (s *Handlers) pageUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sortKey := store.NormalizeUserSort(q.Get("sort"))
	capFilter := store.NormalizeUserCapFilter(q.Get("cap"))
	query := web.Clip(strings.TrimSpace(q.Get("q")), 100)

	var p store.UserListParams
	if c, ok := web.DecodeCursor[userCursor](q.Get("cursor")); ok {
		sortKey = store.NormalizeUserSort(c.Sort)
		capFilter = store.NormalizeUserCapFilter(c.Cap)
		query = c.Query
		p.HasCursor = true
		p.AfterName = c.Name
		p.AfterID = c.ID
		if c.Time != "" {
			p.AfterTime, _ = time.Parse(time.RFC3339Nano, c.Time)
		}
	}
	p.Sort = sortKey
	p.Cap = capFilter
	p.Query = query

	users, hasMore, err := s.Store.ListUsersPage(r.Context(), p)
	if err != nil {
		s.ServerError(w, r, "could not load users", err)
		return
	}
	data := map[string]any{
		"Users": users,
		"Self":  s.Auth.CurrentUserID(r.Context()),
		"Error": q.Get("error"),
		"Sort":  string(sortKey),
		"Cap":   string(capFilter),
		"Query": query,
	}
	if hasMore && len(users) > 0 {
		data["NextCursor"] = web.EncodeCursor(nextUserCursor(p, users[len(users)-1]))
	}
	// htmx "load more": return just the rows + next control to append in place.
	if r.Header.Get("HX-Request") != "" {
		data["Layout"] = "users-frag"
	}
	s.Render(w, r, "admin_users.html", data)
}

// pageUserPermissions renders the per-user permissions modal fragment (the
// manage-users/manage-catalog form), loaded via htmx into the shared modal.
func (s *Handlers) pageUserPermissions(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	u, err := s.Store.GetUserByID(r.Context(), id)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	s.Render(w, r, "admin_user_permissions.html", map[string]any{
		"Account": u,
		"Self":    s.Auth.CurrentUserID(r.Context()),
		"Layout":  "permissions-modal",
	})
}

func (s *Handlers) handleSetUserCaps(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	manageUsers := r.FormValue("manage_users") != ""
	manageCatalog := r.FormValue("manage_catalog") != ""

	// Guard against removing the last user-manager.
	if !manageUsers {
		target, err := s.Store.GetUserByID(r.Context(), id)
		if err != nil {
			s.NotFound(w, r)
			return
		}
		if target.CapManageUsers {
			n, err := s.Store.CountManageUsers(r.Context())
			if err != nil {
				s.ServerError(w, r, "could not check administrators", err)
				return
			}
			if n <= 1 {
				web.RedirectErr(w, r, "/admin/users", "Can't remove the last user manager.")
				return
			}
		}
	}
	if err := s.Store.SetCapabilities(r.Context(), id, manageUsers, manageCatalog); err != nil {
		s.ServerError(w, r, "could not save", err)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// usernameHistoryLimit bounds the rename history shown on the admin page.
const usernameHistoryLimit = 100

// pageUserHistory shows a user's username-change history. Admin-only (mounted
// under the manage-users capability): old usernames are never exposed elsewhere.
func (s *Handlers) pageUserHistory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	u, err := s.Store.GetUserByID(r.Context(), id)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	changes, err := s.Store.ListUsernameChanges(r.Context(), id, usernameHistoryLimit)
	if err != nil {
		s.ServerError(w, r, "could not load history", err)
		return
	}
	data := map[string]any{
		"Account": u,
		"Changes": changes,
	}
	// Loaded into the shared modal via htmx; render just the modal body then.
	if r.Header.Get("HX-Request") != "" {
		data["Layout"] = "history-modal"
	}
	s.Render(w, r, "admin_user_history.html", data)
}
