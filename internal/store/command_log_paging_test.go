package store

import (
	"fmt"
	"strings"
	"testing"
)

// TestListCommandLogSessionsPage walks the keyset-paginated session log and
// checks every session with commands appears once, newest-first, across pages —
// and that a session with no commands is omitted.
func TestListCommandLogSessionsPage(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	owner, err := st.CreateUser(ctx, "logowner", "")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: owner.ID, Name: "R", PublicKeyHex: strings.Repeat("c", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	// More sessions than a page, each with a couple of commands.
	total := CommandLogPageSize*2 + 3
	for i := 0; i < total; i++ {
		sid, err := st.StartConsoleSession(ctx, rep.ID, owner.ID)
		if err != nil {
			t.Fatalf("start session %d: %v", i, err)
		}
		for j := 0; j < 2; j++ {
			if _, err := st.LogCommand(ctx, rep.ID, owner.ID, sid, 0, fmt.Sprintf("cmd %d-%d", i, j)); err != nil {
				t.Fatalf("log command: %v", err)
			}
		}
	}
	// A session with no commands must not show up in the log.
	if _, err := st.StartConsoleSession(ctx, rep.ID, owner.ID); err != nil {
		t.Fatal(err)
	}

	var (
		seen   = map[int64]bool{}
		count  int
		cursor *CommandLogCursor
		pages  int
	)
	var prev *CommandLogCursor
	for {
		page, hasMore, err := st.ListCommandLogSessionsPage(ctx, rep.ID, cursor)
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		pages++
		if len(page) > CommandLogPageSize {
			t.Fatalf("page returned %d sessions, exceeds %d", len(page), CommandLogPageSize)
		}
		for _, g := range page {
			if seen[g.ID] {
				t.Fatalf("session %d returned twice", g.ID)
			}
			seen[g.ID] = true
			if len(g.Entries) == 0 {
				t.Fatalf("session %d has no entries (should have been filtered)", g.ID)
			}
			// Strictly descending by (started_at, id).
			if prev != nil {
				if g.StartedAt.After(prev.StartedAt) ||
					(g.StartedAt.Equal(prev.StartedAt) && g.ID >= prev.ID) {
					t.Fatalf("session order violated: (%v,%d) after (%v,%d)",
						g.StartedAt, g.ID, prev.StartedAt, prev.ID)
				}
			}
			prev = &CommandLogCursor{StartedAt: g.StartedAt, ID: g.ID}
			count++
		}
		if !hasMore {
			break
		}
		last := page[len(page)-1]
		cursor = &CommandLogCursor{StartedAt: last.StartedAt, ID: last.ID}
	}

	if count != total {
		t.Errorf("walked %d sessions across %d pages, want %d (empty session excluded)", count, pages, total)
	}
	if pages < 3 {
		t.Errorf("expected at least 3 pages, got %d", pages)
	}
}
