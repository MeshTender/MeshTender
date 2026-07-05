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

	cmds := post(t, ts, h.app, share+"/"+tid+"/commands", url.Values{}, sess) // empty = no commands
	cmds.Body.Close()
	assertRedirect(t, cmds, share, "set share commands")

	stew := post(t, ts, h.app, share+"/"+tid+"/steward", url.Values{"steward": {"1"}}, sess)
	stew.Body.Close()
	assertRedirect(t, stew, share, "set steward")

	un := post(t, ts, h.app, "/repeaters/"+pid+"/unshare", url.Values{"user_id": {tid}}, sess)
	un.Body.Close()
	assertRedirect(t, un, share, "unshare")

	// #87 participation: exclude this repeater from an org the owner belongs to.
	org, err := st.CreateOrg(ctx, "Participation Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The {orgID} route param is the org slug, not the numeric id.
	part := post(t, ts, h.app, "/repeaters/"+pid+"/orgs/"+org.Slug+"/participation",
		url.Values{"action": {"exclude"}}, sess)
	part.Body.Close()
	assertRedirect(t, part, share, "org participation")
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
	token, err := st.CreateInvite(ctx, rep.ID, "come join")
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
