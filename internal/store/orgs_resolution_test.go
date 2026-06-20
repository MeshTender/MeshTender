package store

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
)

// orgTestStore opens the test DB (gated, *_test only) and wipes mutable state,
// preserving the migration-seeded command_catalog.
func orgTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dsn := os.Getenv("MESHTENDER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set MESHTENDER_TEST_DATABASE_URL to run org resolution tests")
	}
	if u, err := url.Parse(dsn); err != nil || !strings.HasSuffix(strings.TrimPrefix(u.Path, "/"), "_test") {
		t.Fatalf("refusing to run: test DB name must end in _test (got %q)", dsn)
	}
	ctx := context.Background()
	st, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Wipe state but keep command_catalog (seeded by migration).
	if _, err := st.pool.Exec(ctx,
		`TRUNCATE users, repeaters, organizations RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return st, ctx
}

func TestOrgCommandResolution(t *testing.T) {
	st, ctx := orgTestStore(t)

	cmdID := func(key string) int64 {
		var id int64
		if err := st.pool.QueryRow(ctx, `SELECT id FROM command_catalog WHERE key=$1`, key).Scan(&id); err != nil {
			t.Fatalf("command %q: %v", key, err)
		}
		return id
	}
	setRadio, advert, erase, setTx := cmdID("set.radio"), cmdID("advert"), cmdID("erase"), cmdID("set.tx")

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
	check("owner/erase", can(owner, erase), true)
	// Org-admin: admin tier + member tier (⊇), but not commands outside policy.
	check("admin/set.radio", can(adminM, setRadio), true)
	check("admin/advert", can(adminM, advert), true)
	check("admin/erase", can(adminM, erase), false)
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
