package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jleight/meshtender/internal/testdb"
)

// orgTestStore returns a Store backed by a fresh, throwaway database cloned from
// the migrated template (see internal/testdb). Each call gets pristine state —
// command_catalog seeded, everything else empty — so tests need no truncation
// and can run in parallel.
func orgTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := New(ctx, testdb.Fresh(t, migrateTemplate))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(st.Close)
	return st, ctx
}

// migrateTemplate applies the schema to the template database. It opens its own
// store and closes it before returning, so no connection lingers on the
// template when it's cloned.
func migrateTemplate(dsn string) error {
	ctx := context.Background()
	st, err := New(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.Migrate(ctx)
}

func TestOrgCommandResolution(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	cmdID := func(key string) int64 {
		var id int64
		if err := st.pool.QueryRow(ctx, `SELECT id FROM command_catalog WHERE key=$1`, key).Scan(&id); err != nil {
			t.Fatalf("command %q: %v", key, err)
		}
		return id
	}
	// poweroff is a risky, owner-only command kept out of both org tiers — used to
	// check that even an org admin can't run a command outside the site ceiling.
	setRadio, advert, poweroff, setTx := cmdID("set.radio"), cmdID("advert"), cmdID("poweroff"), cmdID("set.tx")

	// Set the site ceiling: admin tier = {set.radio, set.tx}, member tier = {advert},
	// poweroff in neither. (risky/share flags don't matter for these checks.)
	setCeiling := func(id int64, member, admin bool) {
		if err := st.UpdateCommandFlags(ctx, id, false, false, member, admin); err != nil {
			t.Fatalf("set ceiling %d: %v", id, err)
		}
	}
	setCeiling(setRadio, false, true)
	setCeiling(setTx, false, true)
	setCeiling(advert, true, false)
	setCeiling(poweroff, false, false)

	mkUser := func(name string) int64 {
		u, err := st.CreateUser(ctx, name, "")
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return u.ID
	}
	owner := mkUser("owner")
	adminM := mkUser("adminm")
	plainM := mkUser("plainm")
	outsider := mkUser("outsider")

	rep, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: owner, Name: "R", PublicKeyHex: strings.Repeat("a", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}

	org, err := st.CreateOrg(ctx, "Region", owner) // owner is org-admin
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := st.AddOrgMember(ctx, org.ID, adminM, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, plainM, "member"); err != nil {
		t.Fatal(err)
	}
	// The owner's repeater participates in the org automatically (no opt-out).

	can := func(u, c int64) bool {
		ok, err := st.CanSendCommand(ctx, u, rep.ID, c)
		if err != nil {
			t.Fatalf("CanSendCommand: %v", err)
		}
		return ok
	}
	check := func(label string, got, want bool) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %v, want %v", label, got, want)
		}
	}

	// Owner: anything.
	check("owner/poweroff", can(owner, poweroff), true)
	// Org-admin: admin tier + member tier (⊇), but not commands outside the ceiling.
	check("admin/set.radio", can(adminM, setRadio), true)
	check("admin/advert", can(adminM, advert), true)
	check("admin/set.tx", can(adminM, setTx), true)
	check("admin/poweroff", can(adminM, poweroff), false)
	// Plain member: member tier only.
	check("member/advert", can(plainM, advert), true)
	check("member/set.radio", can(plainM, setRadio), false)
	// Outsider: nothing.
	check("outsider/advert", can(outsider, advert), false)

	// Owner opts the org into only {advert}: the admin loses the admin-tier
	// commands not in the list, but keeps advert.
	if err := st.SetOrgOptIn(ctx, org.ID, owner, []int64{advert}); err != nil {
		t.Fatal(err)
	}
	check("admin/set.radio with opt-in", can(adminM, setRadio), false)
	check("admin/advert with opt-in", can(adminM, advert), true)
	// Clearing the opt-in restores the full ceiling.
	if err := st.SetOrgOptIn(ctx, org.ID, owner, nil); err != nil {
		t.Fatal(err)
	}
	check("admin/set.radio after clear", can(adminM, setRadio), true)

	// Opting the repeater out of the org blocks all org access regardless of tier.
	if err := st.SetRepeaterOrgExcluded(ctx, org.ID, rep.ID, true); err != nil {
		t.Fatal(err)
	}
	check("admin/advert when excluded", can(adminM, advert), false)
	check("member/advert when excluded", can(plainM, advert), false)
	if err := st.SetRepeaterOrgExcluded(ctx, org.ID, rep.ID, false); err != nil {
		t.Fatal(err)
	}
	check("admin/advert when re-included", can(adminM, advert), true)
}

// TestListSendableCommandIDsOrgAdmin reproduces the reported bug: an org admin
// (not the owner, not a steward, with no per-command share) opens the Console for
// another member's participating repeater and sees an EMPTY command sidebar, even
// though they can run those commands manually. The sidebar is built from
// ListSendableCommandIDs; before the fix it only covered owner/steward/share and
// omitted the org-participation path, so it returned nothing here. It must return
// the org ceiling for the admin's tier — and, critically, agree command-for-command
// with the runtime gate CanSendCommand.
func TestListSendableCommandIDsOrgAdmin(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	cmdID := func(key string) int64 {
		var id int64
		if err := st.pool.QueryRow(ctx, `SELECT id FROM command_catalog WHERE key=$1`, key).Scan(&id); err != nil {
			t.Fatalf("command %q: %v", key, err)
		}
		return id
	}
	setRadio, advert, poweroff, setTx := cmdID("set.radio"), cmdID("advert"), cmdID("poweroff"), cmdID("set.tx")
	setCeiling := func(id int64, member, admin bool) {
		if err := st.UpdateCommandFlags(ctx, id, false, false, member, admin); err != nil {
			t.Fatalf("set ceiling %d: %v", id, err)
		}
	}
	setCeiling(setRadio, false, true)
	setCeiling(setTx, false, true)
	setCeiling(advert, true, false)
	setCeiling(poweroff, false, false)

	mkUser := func(name string) int64 {
		u, err := st.CreateUser(ctx, name, "")
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return u.ID
	}
	owner := mkUser("owner3")
	adminM := mkUser("adminm3")
	plainM := mkUser("plainm3")
	outsider := mkUser("outsider3")

	rep, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: owner, Name: "R", PublicKeyHex: strings.Repeat("c", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	org, err := st.CreateOrg(ctx, "Region", owner) // owner is org-admin
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := st.AddOrgMember(ctx, org.ID, adminM, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, plainM, "member"); err != nil {
		t.Fatal(err)
	}

	sendable := func(u int64) map[int64]bool {
		ids, err := st.ListSendableCommandIDs(ctx, u, rep.ID)
		if err != nil {
			t.Fatalf("ListSendableCommandIDs: %v", err)
		}
		set := make(map[int64]bool, len(ids))
		for _, id := range ids {
			set[id] = true
		}
		return set
	}

	// The bug: this set was empty. It must be the admin tier (⊇ member tier).
	adminSet := sendable(adminM)
	if len(adminSet) == 0 {
		t.Fatal("org admin sidebar is empty — regression: org-participation path missing")
	}
	for _, c := range []int64{setRadio, setTx, advert} {
		if !adminSet[c] {
			t.Errorf("admin sendable missing command %d", c)
		}
	}
	if adminSet[poweroff] {
		t.Error("admin sendable includes poweroff (outside ceiling)")
	}

	// Plain member: member tier only. Outsider: nothing.
	memberSet := sendable(plainM)
	if !memberSet[advert] || memberSet[setRadio] || memberSet[poweroff] {
		t.Errorf("member sendable = %v, want only advert", memberSet)
	}
	if len(sendable(outsider)) != 0 {
		t.Error("outsider sendable is non-empty")
	}

	// Invariant: the sidebar list and the runtime gate must agree for EVERY
	// (user, command) pair — this is what prevents the two from drifting again.
	allCmds := []int64{setRadio, setTx, advert, poweroff}
	for _, u := range []int64{owner, adminM, plainM, outsider} {
		set := sendable(u)
		for _, c := range allCmds {
			can, err := st.CanSendCommand(ctx, u, rep.ID, c)
			if err != nil {
				t.Fatalf("CanSendCommand: %v", err)
			}
			if can != set[c] {
				t.Errorf("disagreement user=%d cmd=%d: CanSendCommand=%v sidebar=%v", u, c, can, set[c])
			}
		}
	}
}

func TestOrgRepeaterAccess(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, _ := st.CreateUser(ctx, "owner2", "")
	member, _ := st.CreateUser(ctx, "member2", "")
	outsider, _ := st.CreateUser(ctx, "outsider2", "")
	rep, err := st.CreateRepeater(ctx, &Repeater{OwnerID: owner.ID, Name: "R", PublicKeyHex: strings.Repeat("b", 64), RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5})
	if err != nil {
		t.Fatal(err)
	}
	org, _ := st.CreateOrg(ctx, "Org", owner.ID)
	_ = st.AddOrgMember(ctx, org.ID, member.ID, "member")
	// The owner's repeater participates in the org automatically.

	// Member can fetch (and thus operate) the repeater via org access...
	if _, err := st.GetRepeaterForUser(ctx, member.ID, rep.ID); err != nil {
		t.Errorf("member GetRepeaterForUser: %v", err)
	}
	// ...but org repeaters are reached from the org page, not the dashboard list.
	list, err := st.ListRepeatersForUser(ctx, member.ID)
	if err != nil || len(list) != 0 {
		t.Errorf("member dashboard list = %d repeaters (err %v), want 0", len(list), err)
	}
	// Outsider cannot.
	if _, err := st.GetRepeaterForUser(ctx, outsider.ID, rep.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("outsider GetRepeaterForUser = %v, want ErrNotFound", err)
	}
}
