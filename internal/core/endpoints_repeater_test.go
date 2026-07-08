package core

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/jleight/meshtender/internal/store"
)

// TestCleanRepeaterName pins the firmware-derived byte bound: MeshCore stores a
// node name in a 32-byte buffer and NUL-terminates it, leaving 31 usable bytes.
func TestCleanRepeaterName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"  Repeater One  ", "Repeater One", true},
		{"", "", false},
		{"   ", "", false},
		{strings.Repeat("x", 31), strings.Repeat("x", 31), true},
		{strings.Repeat("x", 32), strings.Repeat("x", 32), false},
		{"  " + strings.Repeat("x", 31) + "  ", strings.Repeat("x", 31), true}, // trimmed to the limit
	}
	for _, c := range cases {
		got, ok := cleanRepeaterName(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("cleanRepeaterName(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

// TestRepeaterNameLengthBound: a name over MeshCore's 31-byte limit is rejected
// at create (it would be silently truncated on the device); exactly-at-limit
// succeeds. Regression for the pre-release audit finding that names were
// unbounded.
func TestRepeaterNameLengthBound(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	_, sess := appLogin(t, ts, st, ctx, h.app, "namelen")

	form := func(name, key string) url.Values {
		return url.Values{
			"name": {name}, "public_key": {key},
			"radio_freq_mhz": {"869.525"}, "radio_bw_khz": {"250"}, "radio_sf": {"11"}, "radio_cr": {"5"},
		}
	}

	overID, _ := meshcore.GenerateLocalIdentity(rand.Reader)
	over := post(t, ts, h.app, "/repeaters", form(strings.Repeat("x", maxRepeaterNameLen+1), overID.String()), sess)
	over.Body.Close()
	if loc, _ := url.Parse(over.Header.Get("Location")); over.StatusCode != http.StatusSeeOther || loc.Path != "/repeaters/add" || loc.Query().Get("error") == "" {
		t.Fatalf("over-long create = %d %q, want 303 → /repeaters/add?error", over.StatusCode, over.Header.Get("Location"))
	}

	atID, _ := meshcore.GenerateLocalIdentity(rand.Reader)
	at := post(t, ts, h.app, "/repeaters", form(strings.Repeat("x", maxRepeaterNameLen), atID.String()), sess)
	at.Body.Close()
	if loc, _ := url.Parse(at.Header.Get("Location")); at.StatusCode != http.StatusSeeOther || !strings.HasSuffix(loc.Path, "/added") {
		t.Fatalf("at-limit create = %d %q, want 303 → /repeaters/{id}/added", at.StatusCode, at.Header.Get("Location"))
	}
}

// Black-box coverage for the repeater, sharing, invite, and admin POST endpoints.
// All redirect (303) and do not render; each test asserts the redirect target and,
// where cheap, a store side-effect.

// newOwnedRepeater creates a repeater owned by ownerID with a valid MeshCore key.
func newOwnedRepeater(t *testing.T, st *store.Store, ctx context.Context, ownerID int64, name string) *store.Repeater {
	t.Helper()
	id, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: ownerID, Name: name, PublicKeyHex: id.String(),
		RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	return rep
}

// assertRedirect asserts resp is a 303 whose Location path equals want.
func assertRedirect(t *testing.T, resp *http.Response, want, label string) {
	t.Helper()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusSeeOther || loc.Path != want {
		t.Fatalf("%s = %d %q, want 303 → %s", label, resp.StatusCode, resp.Header.Get("Location"), want)
	}
}

// #63 create, #67 edit, #76 docs, #78 add + #79 delete maintenance, #69 delete.
func TestRepeaterCrudPosts(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "reptester")

	// #63 create → /repeaters/{id}/added
	id, _ := meshcore.GenerateLocalIdentity(rand.Reader)
	create := post(t, ts, h.app, "/repeaters", url.Values{
		"name": {"New Rep"}, "public_key": {id.String()},
		"radio_freq_mhz": {"869.525"}, "radio_bw_khz": {"250"}, "radio_sf": {"11"}, "radio_cr": {"5"},
	}, sess)
	create.Body.Close()
	loc, _ := url.Parse(create.Header.Get("Location"))
	if create.StatusCode != http.StatusSeeOther || loc.Path == "" || loc.Path[len(loc.Path)-6:] != "/added" {
		t.Fatalf("create repeater = %d %q, want 303 → /repeaters/{id}/added", create.StatusCode, create.Header.Get("Location"))
	}

	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Edit Me")
	pid := rep.PublicID

	edit := post(t, ts, h.app, "/repeaters/"+pid+"/edit", url.Values{
		"name": {"Edited"}, "radio_freq_mhz": {"869.525"}, "radio_bw_khz": {"250"}, "radio_sf": {"11"}, "radio_cr": {"5"},
	}, sess)
	edit.Body.Close()
	assertRedirect(t, edit, "/", "edit repeater")

	docs := post(t, ts, h.app, "/repeaters/"+pid+"/docs",
		url.Values{"doc_public": {"public notes"}, "doc_internal": {"internal notes"}}, sess)
	docs.Body.Close()
	assertRedirect(t, docs, "/repeaters/"+pid+"/docs", "save docs")

	maint := post(t, ts, h.app, "/repeaters/"+pid+"/maintenance", url.Values{"note": {"swapped antenna"}}, sess)
	maint.Body.Close()
	assertRedirect(t, maint, "/repeaters/"+pid+"/maintenance", "add maintenance")

	entries, err := st.ListMaintenance(ctx, rep.ID)
	if err != nil || len(entries) == 0 {
		t.Fatalf("list maintenance: %v (n=%d)", err, len(entries))
	}
	del := post(t, ts, h.app, "/repeaters/"+pid+"/maintenance/delete",
		url.Values{"entry_id": {strconv.FormatInt(entries[0].ID, 10)}}, sess)
	del.Body.Close()
	assertRedirect(t, del, "/repeaters/"+pid+"/maintenance", "delete maintenance")

	delRep := post(t, ts, h.app, "/repeaters/"+pid+"/delete", url.Values{}, sess)
	delRep.Body.Close()
	assertRedirect(t, delRep, "/", "delete repeater")
}

// TestAddRepeaterExposePublicPage: the add form's "Publish a public page"
// checkbox is honored at creation (not only on the edit page). Present → the
// repeater publishes a public page; absent → it does not.
func TestAddRepeaterExposePublicPage(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	_, sess := appLogin(t, ts, st, ctx, h.app, "exposer")

	exposed := func(t *testing.T, values url.Values) bool {
		t.Helper()
		resp := post(t, ts, h.app, "/repeaters", values, sess)
		resp.Body.Close()
		loc, _ := url.Parse(resp.Header.Get("Location"))
		parts := strings.Split(loc.Path, "/")
		if resp.StatusCode != http.StatusSeeOther || len(parts) < 3 {
			t.Fatalf("create repeater = %d %q, want 303 → /repeaters/{id}/added", resp.StatusCode, resp.Header.Get("Location"))
		}
		var got bool
		if err := st.Pool().QueryRow(ctx,
			`SELECT expose_public_page FROM repeaters WHERE public_id = $1`, parts[2]).Scan(&got); err != nil {
			t.Fatalf("read expose_public_page: %v", err)
		}
		return got
	}

	radio := func(v url.Values) url.Values {
		v.Set("radio_freq_mhz", "869.525")
		v.Set("radio_bw_khz", "250")
		v.Set("radio_sf", "11")
		v.Set("radio_cr", "5")
		return v
	}

	onID, _ := meshcore.GenerateLocalIdentity(rand.Reader)
	if !exposed(t, radio(url.Values{
		"name": {"Public Rep"}, "public_key": {onID.String()}, "expose_public_page": {"1"},
	})) {
		t.Error("expose_public_page=1 at add time did not publish a public page")
	}

	offID, _ := meshcore.GenerateLocalIdentity(rand.Reader)
	if exposed(t, radio(url.Values{
		"name": {"Private Rep"}, "public_key": {offID.String()},
	})) {
		t.Error("repeater added without the checkbox should not publish a public page")
	}
}

// #81 create link, #82 delete link, #85 share commands, #86 steward, #83 unshare,
// #87 org participation.
func TestRepeaterSharePosts(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "shareowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Shared Rep")
	pid := rep.PublicID
	share := "/repeaters/" + pid + "/share"

	link := post(t, ts, h.app, share+"/link", url.Values{"description": {"friends"}}, sess)
	link.Body.Close()
	assertRedirect(t, link, share, "create share link")

	invites, err := st.ListInvites(ctx, rep.ID)
	if err != nil || len(invites) == 0 {
		t.Fatalf("list invites: %v (n=%d)", err, len(invites))
	}
	dl := post(t, ts, h.app, share+"/link/delete", url.Values{"invite_id": {strconv.FormatInt(invites[0].ID, 10)}}, sess)
	dl.Body.Close()
	assertRedirect(t, dl, share, "delete share link")

	// A directly-added share to a second user, for the per-share endpoints.
	target, err := st.CreateUser(ctx, "sharee", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddShare(ctx, rep.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	tid := strconv.FormatInt(target.ID, 10)

	// "Manage access" save: steward + command grants in one POST.
	acc := post(t, ts, h.app, share+"/"+tid+"/access", url.Values{"steward": {"1"}}, sess)
	acc.Body.Close()
	assertRedirect(t, acc, share, "save person access")
	if steward, _ := st.IsSteward(ctx, rep.ID, target.ID); !steward {
		t.Fatal("save person access with steward=1 did not make them a steward")
	}

	un := post(t, ts, h.app, "/repeaters/"+pid+"/unshare", url.Values{"user_id": {tid}}, sess)
	un.Body.Close()
	assertRedirect(t, un, share, "unshare")

	// Org participation is exercised via the "manage access" limits save in
	// TestRepeaterOrgLimitsPosts (there is no standalone participation endpoint).
}

// TestSharePageRenders is a full-page render check for the share page (the e2e
// entry point): it must 200 with both the org and person "Manage access" buttons.
func TestSharePageRenders(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "sharepageowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "SP Rep")
	if _, err := st.CreateOrg(ctx, "SP Org", owner.ID); err != nil { // owner is a member → org row renders
		t.Fatal(err)
	}
	sharee, err := st.CreateUser(ctx, "spsharee", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddShare(ctx, rep.ID, sharee.ID); err != nil {
		t.Fatal(err)
	}

	resp := do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/share", sess)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share page status = %d, want 200:\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `data-testid="manage-access"`) {
		t.Fatal("share page missing the org Manage access button")
	}
	if !strings.Contains(body, `data-testid="manage-person"`) {
		t.Fatal("share page missing the person Manage access button")
	}
}

// TestActivityPageLinksSender: the activity log links a session's sender name to
// their public profile (/u/{username}).
func TestActivityPageLinksSender(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "actowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Act Rep")
	sid, err := st.StartConsoleSession(ctx, rep.ID, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.LogCommand(ctx, rep.ID, owner.ID, sid, 0, "advert"); err != nil {
		t.Fatal(err)
	}

	body := readBody(t, do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/log", sess))
	if !strings.Contains(body, `/u/`+owner.Username+`"`) {
		t.Fatalf("activity page didn't link the sender to their profile (/u/%s):\n%s", owner.Username, body)
	}
}

// TestPersonAccessModal covers the per-person "manage access" modal: the GET
// fragment renders the steward toggle + command grid, and the POST saves the
// steward flag and command grants together.
func TestPersonAccessModal(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "paccessowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "PA Rep")
	target, err := st.CreateUser(ctx, "paccessee", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddShare(ctx, rep.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	base := "/repeaters/" + rep.PublicID + "/share/" + strconv.FormatInt(target.ID, 10) + "/access"

	// GET renders the modal fragment (no page chrome): steward switch + cmd boxes.
	frag := readBody(t, do(t, ts, h.app, base, sess))
	if !strings.Contains(frag, "Manage access") || !strings.Contains(frag, `name="steward"`) || !strings.Contains(frag, `name="cmd"`) {
		t.Fatalf("person-access fragment missing expected content:\n%s", frag)
	}
	if strings.Contains(frag, "back-link") {
		t.Fatal("person-access fragment should be modal chrome, not a full page")
	}
	// Footer Save/Revoke reference their separate forms via form= (scrollable body).
	if !strings.Contains(frag, `form="person-access-form"`) || !strings.Contains(frag, `form="person-revoke-form"`) {
		t.Fatal("person-access footer buttons should reference their forms via form=")
	}

	// Save: not a steward, grant exactly one command.
	catalog, err := st.ListCommands(ctx)
	if err != nil || len(catalog) == 0 {
		t.Fatalf("list commands: %v (n=%d)", err, len(catalog))
	}
	grant := catalog[0].ID
	save := post(t, ts, h.app, base, url.Values{"cmd": {strconv.FormatInt(grant, 10)}}, sess)
	save.Body.Close()
	assertRedirect(t, save, "/repeaters/"+rep.PublicID+"/share", "save person access")
	if steward, _ := st.IsSteward(ctx, rep.ID, target.ID); steward {
		t.Fatal("saving without the steward flag left them a steward")
	}
	ids, err := st.ListShareCommandIDs(ctx, rep.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != grant {
		t.Fatalf("granted commands = %v, want [%d]", ids, grant)
	}
}

// TestRepeaterOrgLimitsPosts covers the per-(repeater, org) command-limits modal:
// the GET fragment renders the editor, and the POST restricts / collapses back to
// permissive. This is the share-page home for limits after they moved off the
// org-wide page and became per repeater.
func TestRepeaterOrgLimitsPosts(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "limitowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Limited Rep")
	org, err := st.CreateOrg(ctx, "Limits Org", owner.ID) // owner is an admin member
	if err != nil {
		t.Fatal(err)
	}
	base := "/repeaters/" + rep.PublicID + "/orgs/" + org.Slug + "/limits"

	// The ceiling: commands an org may ever run. Restrict to the first one.
	catalog, err := st.ListCommands(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ceiling []int64
	for _, c := range catalog {
		if c.OrgMemberAllowed || c.OrgAdminAllowed {
			ceiling = append(ceiling, c.ID)
		}
	}
	if len(ceiling) < 2 {
		t.Fatalf("need >=2 ceiling commands, got %d", len(ceiling))
	}

	// GET renders the modal fragment (no page chrome): the Shared switch + cmd boxes.
	frag := readBody(t, do(t, ts, h.app, base, sess))
	if !strings.Contains(frag, "Manage access") || !strings.Contains(frag, `name="cmd"`) || !strings.Contains(frag, `name="include"`) {
		t.Fatalf("manage-access fragment missing expected content:\n%s", frag)
	}
	// Footer buttons live outside the form (referencing it via form=) so the modal
	// body can scroll with header/footer pinned; guard that structure.
	if !strings.Contains(frag, `form="org-access-form"`) {
		t.Fatal("manage-access footer button should reference the form via form= (scrollable body)")
	}
	if strings.Contains(frag, "back-link") {
		t.Fatal("limits fragment should be modal chrome, not a full page")
	}
	// Participating by default: no opted-out warning.
	if strings.Contains(frag, "opted out of") {
		t.Fatal("limits fragment shows the opted-out notice while participating")
	}

	// Opted out: the modal warns that no command applies until re-included, but
	// still lets the owner pre-configure limits.
	if err := st.SetRepeaterOrgExcluded(ctx, org.ID, rep.ID, true); err != nil {
		t.Fatal(err)
	}
	if out := readBody(t, do(t, ts, h.app, base, sess)); !strings.Contains(out, "opted out of") || !strings.Contains(out, `name="cmd"`) {
		t.Fatalf("opted-out limits fragment missing notice or command boxes:\n%s", out)
	}
	if err := st.SetRepeaterOrgExcluded(ctx, org.ID, rep.ID, false); err != nil {
		t.Fatal(err)
	}

	optIn := func() []int64 {
		ids, err := st.RepeaterOrgOptInCommandIDs(ctx, org.ID, rep.ID)
		if err != nil {
			t.Fatalf("opt-in ids: %v", err)
		}
		return ids
	}
	excluded := func() bool {
		ex, err := st.IsRepeaterOrgExcluded(ctx, org.ID, rep.ID)
		if err != nil {
			t.Fatalf("excluded: %v", err)
		}
		return ex
	}

	// Save restricts to exactly the first ceiling command, with the Shared switch on.
	save := post(t, ts, h.app, base, url.Values{"include": {"1"}, "cmd": {strconv.FormatInt(ceiling[0], 10)}}, sess)
	save.Body.Close()
	assertRedirect(t, save, "/repeaters/"+rep.PublicID+"/share", "save limits")
	if got := optIn(); len(got) != 1 || got[0] != ceiling[0] {
		t.Fatalf("opt-in = %v, want [%d]", got, ceiling[0])
	}
	if excluded() {
		t.Fatal("saving with the Shared switch on opted the repeater out")
	}

	// Selecting the full ceiling collapses back to permissive (no rows stored).
	full := url.Values{"include": {"1"}}
	for _, id := range ceiling {
		full.Add("cmd", strconv.FormatInt(id, 10))
	}
	fullResp := post(t, ts, h.app, base, full, sess)
	fullResp.Body.Close()
	assertRedirect(t, fullResp, "/repeaters/"+rep.PublicID+"/share", "save full ceiling")
	if got := optIn(); len(got) != 0 {
		t.Fatalf("full selection should store nothing (permissive), got %v", got)
	}

	// Restrict again, then "Remove restriction" clears it (switch still on).
	post(t, ts, h.app, base, url.Values{"include": {"1"}, "cmd": {strconv.FormatInt(ceiling[0], 10)}}, sess).Body.Close()
	clr := post(t, ts, h.app, base, url.Values{"include": {"1"}, "clear": {"1"}}, sess)
	clr.Body.Close()
	assertRedirect(t, clr, "/repeaters/"+rep.PublicID+"/share", "clear limits")
	if got := optIn(); len(got) != 0 {
		t.Fatalf("clear should remove all rows, got %v", got)
	}

	// The Shared switch drives participation: saving with it off opts out; on opts in.
	off := post(t, ts, h.app, base, url.Values{}, sess) // switch off (no include field)
	off.Body.Close()
	assertRedirect(t, off, "/repeaters/"+rep.PublicID+"/share", "save opted out")
	if !excluded() {
		t.Fatal("saving with the Shared switch off did not opt the repeater out")
	}
	on := post(t, ts, h.app, base, url.Values{"include": {"1"}}, sess)
	on.Body.Close()
	assertRedirect(t, on, "/repeaters/"+rep.PublicID+"/share", "save opted in")
	if excluded() {
		t.Fatal("saving with the Shared switch on did not re-include the repeater")
	}
}

// TestRepeaterAddedPageOrgAccess: the add-repeater wizard's final step lists the
// owner's orgs with the shared "Manage access" control (same as the share page),
// not the removed one-click /participation opt-out.
func TestRepeaterAddedPageOrgAccess(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "addedowner")
	org, err := st.CreateOrg(ctx, "Added Org", owner.ID) // owner is a member
	if err != nil {
		t.Fatal(err)
	}
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Added Rep")

	body := readBody(t, do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/added", sess))
	if !strings.Contains(body, org.Name) {
		t.Fatalf("added page missing the owner's org %q:\n%s", org.Name, body)
	}
	if !strings.Contains(body, `data-testid="manage-access"`) || !strings.Contains(body, `id="limits-modal"`) {
		t.Fatal("added page missing the Manage access control / modal")
	}
	// The old dead route must be gone.
	if strings.Contains(body, "/participation") {
		t.Fatal("added page still references the removed /participation endpoint")
	}
}

// #88 POST /invite/{token}/accept — a second user redeems a share link.
func TestAcceptInvitePost(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, err := st.CreateUser(ctx, "invowner", "")
	if err != nil {
		t.Fatal(err)
	}
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Invite Rep")
	token, err := st.CreateInvite(ctx, rep.ID, "come join", nil)
	if err != nil {
		t.Fatal(err)
	}
	invitee, sess := appLogin(t, ts, st, ctx, h.app, "invitee")

	acc := post(t, ts, h.app, "/invite/"+token+"/accept", url.Values{}, sess)
	acc.Body.Close()
	assertRedirect(t, acc, "/", "accept invite")

	// The invitee now sees the repeater in their list (shared).
	reps, err := st.ListRepeatersForUser(ctx, invitee.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rp := range reps {
		if rp.ID == rep.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("accepting the invite did not share the repeater with the invitee")
	}
}

// TestCreateInviteWithCommands covers the "Create single-use link" modal: the GET
// fragment renders the description + command grid, and the POST persists the chosen
// initial grant on the invite so AcceptInvite can seed exactly it.
func TestCreateInviteWithCommands(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "invmodalowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Modal Rep")

	// GET renders the modal fragment (no page chrome) with the description + boxes.
	frag := readBody(t, do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/share/link/new", sess))
	if !strings.Contains(frag, `name="description"`) || !strings.Contains(frag, `name="cmd"`) {
		t.Fatalf("new-invite fragment missing expected fields:\n%s", frag)
	}
	// Footer button references the form via form= so the modal body scrolls.
	if !strings.Contains(frag, `form="new-invite-form"`) {
		t.Fatal("new-invite footer button should reference the form via form= (scrollable body)")
	}
	if strings.Contains(frag, "back-link") {
		t.Fatal("new-invite fragment should be modal chrome, not a full page")
	}

	catalog, err := st.ListCommands(ctx)
	if err != nil || len(catalog) == 0 {
		t.Fatalf("list commands: %v (n=%d)", err, len(catalog))
	}
	grant := catalog[0].ID

	share := "/repeaters/" + rep.PublicID + "/share"
	create := post(t, ts, h.app, share+"/link",
		url.Values{"description": {"for Bob"}, "cmd": {strconv.FormatInt(grant, 10)}}, sess)
	create.Body.Close()
	assertRedirect(t, create, share, "create invite with commands")

	invites, err := st.ListInvites(ctx, rep.ID)
	if err != nil || len(invites) != 1 {
		t.Fatalf("ListInvites = %d, %v; want 1", len(invites), err)
	}
	// The chosen grant is recorded on the invite (seeded on accept).
	var got []int64
	rows, err := st.Pool().Query(ctx, `SELECT command_id FROM invite_commands WHERE invite_id = $1`, invites[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if len(got) != 1 || got[0] != grant {
		t.Fatalf("invite_commands = %v, want [%d]", got, grant)
	}
}

// #92 catalog update, #95 set user capabilities. Both require an admin cap, which
// the session picks up live from the store on the next request.
func TestAdminPosts(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "superadmin")
	if err := st.SetCapabilities(ctx, admin.ID, true, true); err != nil {
		t.Fatal(err)
	}

	cmds, err := st.ListCommands(ctx)
	if err != nil || len(cmds) == 0 {
		t.Fatalf("list commands: %v (n=%d)", err, len(cmds))
	}
	cat := post(t, ts, h.app, "/admin/catalog/"+strconv.FormatInt(cmds[0].ID, 10), url.Values{}, sess)
	cat.Body.Close()
	assertRedirect(t, cat, "/admin/catalog", "catalog update")

	target, err := st.CreateUser(ctx, "capuser", "")
	if err != nil {
		t.Fatal(err)
	}
	setCaps := post(t, ts, h.app, "/admin/users/"+strconv.FormatInt(target.ID, 10),
		url.Values{"manage_catalog": {"1"}}, sess)
	setCaps.Body.Close()
	assertRedirect(t, setCaps, "/admin/users", "set user caps")
}
