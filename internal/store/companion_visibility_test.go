package store

import (
	"context"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

// migrateTo moves the database behind st to exactly version v.
func migrateTo(t *testing.T, st *Store, ctx context.Context, v int64) {
	t.Helper()
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	db := st.migrationDB()
	defer func() { _ = db.Close() }()
	p, err := goose.NewProvider(goose.DialectPostgres, db, sub)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := p.GetDBVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cur > v {
		_, err = p.DownTo(ctx, v)
	} else {
		_, err = p.UpTo(ctx, v)
	}
	if err != nil {
		t.Fatalf("migrate to %d: %v", v, err)
	}
}

// TestMeshCoreLinksBecomePublicCompanions: migration 0054 turns each MeshCore
// profile link into a public companion — or makes the user's own registration of
// that key public — skips keys someone else registered, bad keys, and users at
// the cap, and removes the links. Converted companions survive the cleanup of
// never-heard registrations.
func TestMeshCoreLinksBecomePublicCompanions(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	migrateTo(t, st, ctx, 53)

	alice := mustUser(t, st, ctx, "linkalice")
	bob := mustUser(t, st, ctx, "linkbob")
	carol := mustUser(t, st, ctx, "linkcarol")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	companion := func(owner int64, name string, key int) {
		exec(`INSERT INTO companions (public_id, owner_id, name, public_key_hex) VALUES ($1, $2, $3, $4)`,
			testKey(key)[:12], owner, name, testKey(key))
	}
	link := func(user int64, label, url string, pos int) {
		exec(`INSERT INTO user_links (user_id, platform, label, url, position) VALUES ($1, 'meshcore', $2, $3, $4)`,
			user, label, url, pos)
	}
	companion(alice, "Already Mine", 2)
	companion(bob, "Bob's", 3)
	for i := range MaxCompanionsPerUser {
		companion(carol, "Full", 10+i)
	}
	link(alice, "Base Node", testKey(1), 0)     // new → public companion
	link(alice, "", testKey(2), 1)              // her own registration → made public
	link(alice, "Squatter", testKey(3), 2)      // Bob's key → skipped
	link(alice, "Bad", "not a key", 3)          // invalid → dropped
	link(carol, "Over the cap", testKey(30), 0) // carol is at the cap → skipped
	exec(`INSERT INTO user_links (user_id, platform, url, position) VALUES ($1, 'website', 'https://example.org', 9)`, alice)

	migrateTo(t, st, ctx, 54)

	var meshLinks, otherLinks int
	_ = st.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE platform = 'meshcore'), count(*) FILTER (WHERE platform <> 'meshcore') FROM user_links`).
		Scan(&meshLinks, &otherLinks)
	if meshLinks != 0 || otherLinks != 1 {
		t.Fatalf("links after: %d meshcore, %d other; want 0 and 1", meshLinks, otherLinks)
	}
	pub, err := st.ListPublicCompanions(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(pub) != 2 || pub[0].Name != "Already Mine" || pub[1].Name != "Base Node" ||
		pub[1].PublicKeyHex != testKey(1) || !pub[1].KeepUnconfirmed || pub[0].KeepUnconfirmed {
		t.Fatalf("alice's public companions = %+v", pub)
	}
	if b, _ := st.ListPublicCompanions(ctx, bob); len(b) != 0 {
		t.Errorf("bob's companion was made public: %+v", b)
	}
	if c, _ := st.ListCompanionsForUser(ctx, carol); len(c) != MaxCompanionsPerUser {
		t.Errorf("carol has %d companions, want the cap (%d)", len(c), MaxCompanionsPerUser)
	}

	// A day later, the cleanup of never-heard registrations spares the converted
	// one but not an ordinary registration.
	exec(`UPDATE companions SET created_at = now() - interval '2 days'`)
	if _, err := st.PruneUnconfirmedCompanions(ctx); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.ListCompanionsForUser(ctx, alice); len(left) != 1 || left[0].Name != "Base Node" {
		t.Fatalf("after cleanup alice has %+v, want only the converted one", left)
	}
}

func TestCompanionVisibility(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner := mustUser(t, st, ctx, "visowner")
	other := mustUser(t, st, ctx, "visother")
	c := mustCompanion(t, st, ctx, owner, "Pocket", 40)
	if c.Public {
		t.Fatal("a new companion is public")
	}
	if pub, _ := st.ListPublicCompanions(ctx, owner); len(pub) != 0 {
		t.Fatalf("public before = %+v", pub)
	}
	if err := st.SetCompanionPublic(ctx, other, c.PublicID, true); err == nil {
		t.Fatal("someone else changed its visibility")
	}
	if err := st.SetCompanionPublic(ctx, owner, c.PublicID, true); err != nil {
		t.Fatal(err)
	}
	if pub, _ := st.ListPublicCompanions(ctx, owner); len(pub) != 1 || !pub[0].Public {
		t.Fatalf("public after = %+v", pub)
	}
}
