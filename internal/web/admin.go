package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/store"
)

// requireCap is middleware that allows the request only if the current user
// satisfies pick; otherwise it 404s (without revealing the admin area exists).
func (s *Server) requireCap(pick func(*store.User) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, err := s.store.GetUserByID(r.Context(), s.auth.CurrentUserID(r.Context()))
			if err != nil || !pick(u) {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func capAny(u *store.User) bool     { return u.CapManageUsers || u.CapManageCatalog }
func capCatalog(u *store.User) bool { return u.CapManageCatalog }
func capUsers(u *store.User) bool   { return u.CapManageUsers }

// categoryGroup is a list of per-command view models bucketed under a catalog
// category name. The element type varies per page (raw command, share checkbox,
// per-tier checkbox); groupByCategory builds them all.
type categoryGroup[T any] struct {
	Name     string
	Commands []T
}

// groupByCategory buckets the catalog by category in catalog order, mapping each
// command to a per-page view model via mk. It is the one grouping loop shared by
// the catalog, share-commands, and org-permission editors.
func groupByCategory[T any](catalog []*store.Command, mk func(*store.Command) T) []categoryGroup[T] {
	var groups []categoryGroup[T]
	idx := map[string]int{}
	for _, c := range catalog {
		gi, ok := idx[c.Category]
		if !ok {
			gi = len(groups)
			idx[c.Category] = gi
			groups = append(groups, categoryGroup[T]{Name: c.Category})
		}
		groups[gi].Commands = append(groups[gi].Commands, mk(c))
	}
	return groups
}

// catalogGroup buckets raw catalog commands for the admin catalog page.
type catalogGroup = categoryGroup[*store.Command]

func groupCatalog(catalog []*store.Command) []catalogGroup {
	return groupByCategory(catalog, func(c *store.Command) *store.Command { return c })
}

func (s *Server) pageAdmin(w http.ResponseWriter, r *http.Request) {
	u, _ := s.store.GetUserByID(r.Context(), s.auth.CurrentUserID(r.Context()))
	s.render(w, r, "admin.html", map[string]any{
		"CanCatalog": u.CapManageCatalog,
		"CanUsers":   u.CapManageUsers,
	})
}

func (s *Server) pageCatalog(w http.ResponseWriter, r *http.Request) {
	catalog, err := s.store.ListCommands(r.Context())
	if err != nil {
		http.Error(w, "could not load catalog", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "admin_catalog.html", map[string]any{
		"Groups": groupCatalog(catalog),
		"Saved":  r.URL.Query().Get("saved") != "",
	})
}

func (s *Server) handleUpdateCommand(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	on := func(name string) bool { return r.FormValue(name) != "" }
	if err := s.store.UpdateCommandFlags(r.Context(), id,
		on("risky"), on("share"), on("org_member"), on("org_admin")); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/catalog?saved=1", http.StatusSeeOther)
}

func (s *Server) pageUsers(w http.ResponseWriter, r *http.Request) {
	after := r.URL.Query().Get("after")
	users, hasMore, err := s.store.ListUsersPage(r.Context(), after)
	if err != nil {
		http.Error(w, "could not load users", http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"Users": users,
		"Self":  s.auth.CurrentUserID(r.Context()),
		"Error": r.URL.Query().Get("error"),
	}
	if hasMore && len(users) > 0 {
		data["NextAfter"] = users[len(users)-1].Username
	}
	s.render(w, r, "admin_users.html", data)
}

func (s *Server) handleSetUserCaps(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	manageUsers := r.FormValue("manage_users") != ""
	manageCatalog := r.FormValue("manage_catalog") != ""

	// Guard against removing the last user-manager.
	if !manageUsers {
		target, err := s.store.GetUserByID(r.Context(), id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if target.CapManageUsers {
			n, err := s.store.CountManageUsers(r.Context())
			if err != nil {
				http.Error(w, "error", http.StatusInternalServerError)
				return
			}
			if n <= 1 {
				redirectErr(w, r, "/admin/users", "Can't remove the last user manager.")
				return
			}
		}
	}
	if err := s.store.SetCapabilities(r.Context(), id, manageUsers, manageCatalog); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}
