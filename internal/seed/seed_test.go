package seed

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/testdb"
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
}
