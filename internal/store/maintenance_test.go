package store

import (
	"strings"
	"testing"
	"time"
)

// TestListMaintenanceUsesLiveAuthorName is the regression for the maintenance
// page showing a *stale* author name: ListMaintenance must resolve the author's
// current display name, not the name snapshotted into author_name at write
// time. It also covers the username fallback when the display name is cleared.
func TestListMaintenanceUsesLiveAuthorName(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	author, err := st.CreateUser(ctx, "alice", "Alice One")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: author.ID, Name: "R", PublicKeyHex: strings.Repeat("a", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Snapshot the name at write time, exactly as handleAddMaintenance does.
	if err := st.AddMaintenanceEntry(ctx, rep.ID, author.ID, "Alice One", "swapped antenna", time.Now()); err != nil {
		t.Fatal(err)
	}

	nameNow := func() string {
		t.Helper()
		es, err := st.ListMaintenance(ctx, rep.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(es) != 1 {
			t.Fatalf("want 1 entry, got %d", len(es))
		}
		return es[0].AuthorName
	}

	if got := nameNow(); got != "Alice One" {
		t.Fatalf("initial: got %q, want %q", got, "Alice One")
	}

	// Rename the author. The entry must now reflect the NEW display name — this
	// is the bug: before the fix ListMaintenance returns the stale "Alice One".
	if err := st.SetDisplayName(ctx, author.ID, "Alice Renamed"); err != nil {
		t.Fatal(err)
	}
	if got := nameNow(); got != "Alice Renamed" {
		t.Fatalf("after rename: got %q, want %q (stale snapshot returned)", got, "Alice Renamed")
	}

	// Clearing the display name falls back to the username, matching User.Name().
	if err := st.SetDisplayName(ctx, author.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := nameNow(); got != "alice" {
		t.Fatalf("after clearing display name: got %q, want username %q", got, "alice")
	}
}

// TestListMaintenanceDeletedAuthorFallsBackToSnapshot verifies the denormalized
// author_name still shows once the author row is gone (author_id NULL) — the
// tombstone the column exists for. There is no DeleteUser store method, so the
// NULL-author row is inserted directly.
func TestListMaintenanceDeletedAuthorFallsBackToSnapshot(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	owner, err := st.CreateUser(ctx, "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: owner.ID, Name: "R", PublicKeyHex: strings.Repeat("b", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A maintenance row whose author has since been deleted: author_id NULL,
	// author_name retained as the readable tombstone.
	if _, err := st.pool.Exec(ctx, `
		INSERT INTO repeater_maintenance (repeater_id, author_id, author_name, note)
		VALUES ($1, NULL, $2, $3)`, rep.ID, "Departed Builder", "site visit"); err != nil {
		t.Fatal(err)
	}

	es, err := st.ListMaintenance(ctx, rep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 {
		t.Fatalf("want 1 entry, got %d", len(es))
	}
	if es[0].AuthorID != nil {
		t.Fatalf("author_id: got %d, want nil", *es[0].AuthorID)
	}
	if es[0].AuthorName != "Departed Builder" {
		t.Fatalf("deleted-author fallback: got %q, want %q", es[0].AuthorName, "Departed Builder")
	}
}
