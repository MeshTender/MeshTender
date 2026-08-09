package seed

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.RunMain(m)) }

func TestRunSeeds(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Fresh(t, func(dsn string) error {
		st, err := store.New(ctx, dsn)
		if err != nil {
			return err
		}
		defer st.Close()
		return st.Migrate(ctx)
	})
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(st.Close)

	if err := Run(ctx, st, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	var users, orgs, reps int
	_ = st.Pool().QueryRow(ctx, "SELECT count(*) FROM users").Scan(&users)
	_ = st.Pool().QueryRow(ctx, "SELECT count(*) FROM organizations").Scan(&orgs)
	_ = st.Pool().QueryRow(ctx, "SELECT count(*) FROM repeaters").Scan(&reps)
	if users < 1 || reps < 1 || orgs < store.OrgsPageSize+1 {
		t.Fatalf("seed counts too low: users=%d orgs=%d reps=%d", users, orgs, reps)
	}

	// The public directory should span more than one page.
	page, more, err := st.ListPublicOrgsPage(ctx, store.OrgListParams{Sort: store.OrgSortMembers})
	if err != nil {
		t.Fatalf("list orgs: %v", err)
	}
	if len(page) != store.OrgsPageSize || !more {
		t.Fatalf("expected a full first page with more pages; got len=%d more=%v", len(page), more)
	}

	// At least some repeaters should be publicly visible on an org page (owner is
	// a member + show_on_public_org), proving the relationships line up.
	var publicOnOrg int
	_ = st.Pool().QueryRow(ctx, `
		SELECT count(*) FROM repeaters r
		JOIN org_members om ON om.user_id = r.owner_id
		WHERE r.show_on_public_org`).Scan(&publicOnOrg)
	if publicOnOrg == 0 {
		t.Fatalf("no repeaters surface on any org public page")
	}

	// Seeded users should have profile data and a spread of links. With 60 users
	// and the seeding odds, all of these being zero is astronomically unlikely.
	var withProfile, totalLinks, primaryLinks, meshLinks int
	_ = st.Pool().QueryRow(ctx, `SELECT count(*) FROM users WHERE bio <> '' OR location <> '' OR callsign <> ''`).Scan(&withProfile)
	_ = st.Pool().QueryRow(ctx, `SELECT count(*) FROM user_links`).Scan(&totalLinks)
	_ = st.Pool().QueryRow(ctx, `SELECT count(*) FROM user_links WHERE is_primary`).Scan(&primaryLinks)
	_ = st.Pool().QueryRow(ctx, `SELECT count(*) FROM user_links WHERE platform = 'meshcore'`).Scan(&meshLinks)
	if withProfile == 0 || totalLinks == 0 || primaryLinks == 0 || meshLinks == 0 {
		t.Fatalf("seed profile data too sparse: withProfile=%d links=%d primary=%d mesh=%d", withProfile, totalLinks, primaryLinks, meshLinks)
	}

	// Orgs also get public links.
	var orgLinks int
	_ = st.Pool().QueryRow(ctx, `SELECT count(*) FROM org_links`).Scan(&orgLinks)
	if orgLinks == 0 {
		t.Fatalf("no org links were seeded")
	}

	// Handle-platform links are stored in canonical URL form (as the editor would),
	// so the public page can link them directly.
	for _, tbl := range []string{"user_links", "org_links"} {
		var bad int
		_ = st.Pool().QueryRow(ctx, `SELECT count(*) FROM `+tbl+` WHERE platform = 'github' AND url NOT LIKE 'https://github.com/%'`).Scan(&bad)
		if bad != 0 {
			t.Fatalf("%s: %d github links are not canonical https://github.com/ URLs", tbl, bad)
		}
	}
}
