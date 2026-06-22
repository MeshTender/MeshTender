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
	// poweroff is a risky, owner-only command that's in no org tier — used to
	// check that even an org admin can't run a command outside the policy.
	setRadio, advert, poweroff, setTx := cmdID("set.radio"), cmdID("advert"), cmdID("poweroff"), cmdID("set.tx")

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

	// Controlled v2: admin={set.radio}, member={advert}; contribute pinned to it.
	if _, err := st.PublishVersion(ctx, org.ID, "v2", owner, []int64{setRadio}, []int64{advert}); err != nil {
		t.Fatal(err)
	}
	vid, _, _ := st.CurrentVersion(ctx, org.ID)
	if err := st.ContributeRepeater(ctx, org.ID, rep.ID, vid, owner); err != nil {
		t.Fatal(err)
	}

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
	// Org-admin: admin tier + member tier (⊇), but not commands outside policy.
	check("admin/set.radio", can(adminM, setRadio), true)
	check("admin/advert", can(adminM, advert), true)
	check("admin/poweroff", can(adminM, poweroff), false)
	// Plain member: member tier only.
	check("member/advert", can(plainM, advert), true)
	check("member/set.radio", can(plainM, setRadio), false)
	// Outsider: nothing.
	check("outsider/advert", can(outsider, advert), false)

	// Add set.tx to admin in v3; repeater still pinned to v2 → blocked until re-consent.
	if _, err := st.PublishVersion(ctx, org.ID, "v3 add set.tx", owner, []int64{setRadio, setTx}, []int64{advert}); err != nil {
		t.Fatal(err)
	}
	check("admin/set.tx before reconsent", can(adminM, setTx), false)
	v3, _, _ := st.CurrentVersion(ctx, org.ID)
	if err := st.ContributeRepeater(ctx, org.ID, rep.ID, v3, owner); err != nil { // re-consent
		t.Fatal(err)
	}
	check("admin/set.tx after reconsent", can(adminM, setTx), true)

	// Remove set.radio in v4 (admin={set.tx}); removal auto-applies even though
	// the repeater is still consented to v3 (which had set.radio).
	if _, err := st.PublishVersion(ctx, org.ID, "v4 drop set.radio", owner, []int64{setTx}, []int64{advert}); err != nil {
		t.Fatal(err)
	}
	check("admin/set.radio after removal", can(adminM, setRadio), false)
	check("admin/set.tx still", can(adminM, setTx), true)
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
	vid, _, _ := st.CurrentVersion(ctx, org.ID)
	_ = st.ContributeRepeater(ctx, org.ID, rep.ID, vid, owner.ID)

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
