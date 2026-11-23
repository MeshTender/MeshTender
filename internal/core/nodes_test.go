package core

import (
	"crypto/rand"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/observe"
	"github.com/MeshTender/MeshTender/internal/observe/observetest"
	"github.com/MeshTender/MeshTender/internal/store"
)

// TestNodeRowsSortByName: one list sorted by name, ignoring case, with a
// companion ahead of a repeater of the same name.
func TestNodeRowsSortByName(t *testing.T) {
	t.Parallel()
	rows := nodeRows(
		[]*store.Companion{{Name: "zed"}, {Name: "Hilltop"}},
		[]*store.Repeater{{Name: "hilltop"}, {Name: "Alpha"}},
		nil,
	)
	var got []string
	for _, r := range rows {
		got = append(got, r.Kind+":"+r.Name)
	}
	want := "repeater:Alpha companion:Hilltop repeater:hilltop companion:zed"
	if strings.Join(got, " ") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
}

func TestParseNodeShow(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"companion": "companion", "repeater": "repeater", "observer": "observer", "shared": "shared", "sensor": "", "": ""} {
		if got := parseNodeShow(url.Values{"show": {in}}); got != want {
			t.Errorf("show=%q → %q, want %q", in, got, want)
		}
	}
}

// newNodesRepeater creates a repeater owned by ownerID.
func newNodesRepeater(t *testing.T, st *store.Store, ownerID int64, name string) *store.Repeater {
	t.Helper()
	id, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := st.CreateRepeater(t.Context(), &store.Repeater{
		OwnerID: ownerID, Name: name, PublicKeyHex: id.String(),
		RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// TestNodesPage: companions and repeaters (owned and shared) in one list by
// name, each tagged for the Show filter; a shared repeater is tagged both
// repeater and shared. ?show= picks the filter's starting choice.
func TestNodesPage(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	user, sess := appLogin(t, ts, st, ctx, h.app, "nodes")
	other, err := st.CreateUser(ctx, "nodesowner", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCompanion(ctx, user.ID, "Zed T1", strings.Repeat("ab", 32)); err != nil {
		t.Fatal(err)
	}
	newNodesRepeater(t, st, user.ID, "Alpha Ridge")
	shared := newNodesRepeater(t, st, other.ID, "Mill Tower")
	if _, err := st.AddShare(ctx, shared.ID, user.ID); err != nil {
		t.Fatal(err)
	}

	body := getBody(t, ts, h.app, "/nodes", sess)
	assertInOrder(t, "node list", body, "Alpha Ridge", "Mill Tower", "Zed T1")
	for _, want := range []string{
		`data-testid="node-show"`, `data-filter-search`, `data-filter-tokens`,
		`data-show="companion"`, `data-show="repeater"`, `data-show="repeater shared"`,
		`>Companion</span>`, `>Repeater</span>`,
		"Shared with you by nodesowner",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("node list lacks %s", want)
		}
	}
	if !strings.Contains(getBody(t, ts, h.app, "/nodes?show=shared", sess), `<option value="shared" selected>`) {
		t.Error("?show=shared doesn't start on Shared with me")
	}
}

// TestNodesPageShapes: a companion-only list (most members) has no filters; a
// repeater-only one has the same search and Show as any other; an empty one
// offers to add a node.
func TestNodesPageShapes(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	companionUser, companionSess := appLogin(t, ts, st, ctx, h.app, "onlycompanions")
	if _, err := st.CreateCompanion(ctx, companionUser.ID, "Pocket T1", strings.Repeat("cd", 32)); err != nil {
		t.Fatal(err)
	}
	if body := getBody(t, ts, h.app, "/nodes", companionSess); !strings.Contains(body, "Pocket T1") ||
		strings.Contains(body, "data-filter-search") || strings.Contains(body, `data-testid="node-show"`) {
		t.Error("companion-only list should show the companion and no filters")
	}

	repeaterUser, repeaterSess := appLogin(t, ts, st, ctx, h.app, "onlyrepeaters")
	newNodesRepeater(t, st, repeaterUser.ID, "Solo Ridge")
	if body := getBody(t, ts, h.app, "/nodes", repeaterSess); !strings.Contains(body, `data-testid="node-show"`) || !strings.Contains(body, "data-filter-search") {
		t.Error("repeater-only list should have search and Show")
	}

	_, emptySess := appLogin(t, ts, st, ctx, h.app, "nonodes")
	if body := getBody(t, ts, h.app, "/nodes", emptySess); !strings.Contains(body, "No nodes yet") || !strings.Contains(body, `href="/nodes/new"`) {
		t.Error("empty list should offer to add a node")
	}
}

// TestNodesListFlagsGuestAccessWithABadgeOnly: a repeater MeshTender only has
// guest access to is flagged in the list by its badge; the explanation and fix
// are on the repeater's own page, not repeated as an alert in every row.
func TestNodesListFlagsGuestAccessWithABadgeOnly(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	user, sess := appLogin(t, ts, st, ctx, h.app, "guestonly")
	rep := newNodesRepeater(t, st, user.ID, "Guest Ridge")
	if err := st.SetRepeaterConfirmed(ctx, rep.ID, user.ID, false, 0); err != nil {
		t.Fatal(err)
	}
	body := getBody(t, ts, h.app, "/nodes", sess)
	if !strings.Contains(body, "Guest access only") {
		t.Fatal("no Guest access only badge; the fixture isn't a guest-only repeater")
	}
	if strings.Contains(body, "alert-danger") {
		t.Error("the list still shows the guest-access alert")
	}
	if !strings.Contains(getBody(t, ts, h.app, "/repeaters/"+rep.PublicID, sess), `data-testid="guest-access"`) {
		t.Error("the repeater's page no longer explains guest access")
	}
}

// TestOldNodeListsRedirect: /companions and /repeaters open the Nodes list
// showing that kind.
func TestOldNodeListsRedirect(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	_, sess := appLogin(t, ts, st, ctx, h.app, "oldlinks")
	for path, want := range map[string]string{"/companions": "/nodes?show=companion", "/repeaters": "/nodes?show=repeater"} {
		resp := do(t, ts, h.app, path, sess)
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != want {
			t.Errorf("GET %s = %d → %q, want 302 → %q", path, resp.StatusCode, resp.Header.Get("Location"), want)
		}
	}
}

// TestAddNodeOffersEachKind: the chooser leads to each kind's own form.
func TestAddNodeOffersEachKind(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	_, sess := appLogin(t, ts, st, ctx, h.app, "addnode")
	body := getBody(t, ts, h.app, "/nodes/new", sess)
	assertInOrder(t, "add node", body, `href="/companions/new"`, `href="/repeaters/add"`, `href="/observers/new"`)
}

// TestNodeRowsWithObservers: an observer whose key is one of the user's
// repeaters rides on the repeater's row instead of getting its own, and an
// observer nobody has heard from yet (no name) sorts after the named rows.
func TestNodeRowsWithObservers(t *testing.T) {
	t.Parallel()
	shared := &store.UserObserver{PublicKeyHex: "bb", Name: "Ridge"}
	unnamed := &store.UserObserver{PublicKeyHex: "aa"}
	named := &store.UserObserver{PublicKeyHex: "cc", Name: "Hilltop"}
	rows := nodeRows(nil, []*store.Repeater{{Name: "Ridge", PublicKeyHex: "bb"}}, []*store.UserObserver{shared, unnamed, named})
	var got []string
	for _, r := range rows {
		got = append(got, r.Kind+":"+r.Name+":"+r.Key)
	}
	if want := "observer:Hilltop:cc repeater:Ridge:bb observer::aa"; strings.Join(got, " ") != want {
		t.Errorf("rows = %v, want %s", got, want)
	}
	if rows[1].Observer != shared {
		t.Error("the repeater's row doesn't carry its observer")
	}
}

// TestNodesListShowsObservers: observers appear in the list (tagged for the
// Show filter's Observers choice), an unheard one by its short key, and one of
// the user's repeaters that's also an observer gets a badge linking to the
// observer's page. Observers alone are enough to bring up the filters.
func TestNodesListShowsObservers(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	user, sess := appLogin(t, ts, st, ctx, h.app, "nodeobservers")
	org, err := st.CreateOrg(ctx, "Obs Mesh", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	heard := observetest.NewIdentity(t).String()
	if _, err := st.CreateUserObserver(ctx, user.ID, heard); err != nil {
		t.Fatal(err)
	}
	if err := st.ReportObserver(ctx, org.ID, heard, "PDX", time.Now(), &observe.Status{Name: "Hilltop Room", FirmwareVersion: "v1.18.0.4313-meshtender-fe351d9f"}); err != nil {
		t.Fatal(err)
	}
	quiet := observetest.NewIdentity(t).String()
	if _, err := st.CreateUserObserver(ctx, user.ID, quiet); err != nil {
		t.Fatal(err)
	}

	body := getBody(t, ts, h.app, "/nodes", sess)
	assertInOrder(t, "node list", body, "Hilltop Room", quiet[:8]+"…")
	for _, want := range []string{
		`data-testid="node-show"`, `<option value="observer"`, `data-show="observer"`,
		"v1.18.0.4313-meshtender-fe351d9f", "Reports to 1 organization",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("node list lacks %s", want)
		}
	}

	rep := newNodesRepeater(t, st, user.ID, "Ridge Repeater")
	o, err := st.CreateUserObserver(ctx, user.ID, rep.PublicKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	body = getBody(t, ts, h.app, "/nodes", sess)
	if !strings.Contains(body, `data-show="repeater observer"`) || !strings.Contains(body, `href="/observers/`+o.PublicID+`" data-testid="node-repeater-observer"`) {
		t.Error("a repeater that's also an observer should be one row with an Observer badge linking to the observer")
	}
	if n := strings.Count(body, `data-testid="node-observer"`); n != 2 {
		t.Errorf("%d observer rows, want 2: the repeater's observer shouldn't get a row of its own", n)
	}
}
