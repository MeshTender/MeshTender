package core

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestAdminUsersFilterSearchAndModals covers the reworked admin users page:
// capability filter + search on the list, and the Permissions/History modal
// fragments the Actions dropdown loads via htmx.
func TestAdminUsersFilterSearchAndModals(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "adminz")
	if err := st.SetCapabilities(ctx, admin.ID, true, true); err != nil {
		t.Fatalf("set caps: %v", err)
	}
	mgr, err := st.CreateUser(ctx, "mgruser", "")
	if err != nil {
		t.Fatalf("create mgr: %v", err)
	}
	if err := st.SetCapabilities(ctx, mgr.ID, true, false); err != nil {
		t.Fatalf("caps: %v", err)
	}
	if _, err := st.CreateUser(ctx, "plainuser", ""); err != nil {
		t.Fatalf("create plain: %v", err)
	}
	mgrID := strconv.FormatInt(mgr.ID, 10)

	// Capability filter: managers only.
	if body := readBody(t, do(t, ts, h.app, "/admin/users?cap=managers", sess)); !strings.Contains(body, "mgruser") || strings.Contains(body, "plainuser") {
		t.Fatal("cap=managers should list managers and exclude non-managers")
	}
	// Search: username substring.
	if body := readBody(t, do(t, ts, h.app, "/admin/users?q=plainuser", sess)); !strings.Contains(body, "plainuser") || strings.Contains(body, "mgruser") {
		t.Fatal("search should show only matching users")
	}

	// Permissions fragment: a save form scoped to the target, prefilled.
	perm := readBody(t, do(t, ts, h.app, "/admin/users/"+mgrID+"/permissions", sess))
	if !strings.Contains(perm, `action="/admin/users/`+mgrID+`"`) {
		t.Fatalf("permissions fragment missing the save form:\n%s", perm)
	}
	if !strings.Contains(perm, `name="manage_users"`) || !strings.Contains(perm, "Permissions —") {
		t.Fatal("permissions fragment missing expected fields")
	}

	// History as an htmx fragment vs. the full page.
	frag := readBody(t, doHX(t, ts, h.app, "/admin/users/"+mgrID+"/history", sess))
	if !strings.Contains(frag, "modal-header") || strings.Contains(frag, "back-link") {
		t.Fatalf("history HX fragment should be modal chrome, not a full page:\n%s", frag)
	}
	full := readBody(t, do(t, ts, h.app, "/admin/users/"+mgrID+"/history", sess))
	if !strings.Contains(full, "back-link") {
		t.Fatal("history full page should render the back-link")
	}
}

// doHX issues a GET with the HX-Request header (an htmx fragment request).
func doHX(t *testing.T, ts *httptest.Server, host, path string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = host
	req.Header.Set("HX-Request", "true")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatalf("hx get %s%s: %v", host, path, err)
	}
	return resp
}
