package store

import (
	"fmt"
	"testing"
)

// TestListUsersPage walks the keyset-paginated admin user list and checks every
// user appears exactly once, in username order, across pages.
func TestListUsersPage(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t) // reuses the gated *_test store + truncation

	total := UsersPageSize*2 + 3
	for i := 0; i < total; i++ {
		// Zero-padded usernames give a deterministic lexical order to assert.
		if _, err := st.CreateUser(ctx, fmt.Sprintf("user-%04d", i), ""); err != nil {
			t.Fatalf("create user %d: %v", i, err)
		}
	}

	var (
		seen     = map[int64]bool{}
		count    int
		p        = UserListParams{Sort: UserSortName}
		prevName string
		pages    int
	)
	for {
		page, hasMore, err := st.ListUsersPage(ctx, p)
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		pages++
		if len(page) > UsersPageSize {
			t.Fatalf("page returned %d rows, exceeds UsersPageSize %d", len(page), UsersPageSize)
		}
		for _, u := range page {
			if seen[u.ID] {
				t.Fatalf("user %d (%q) returned twice", u.ID, u.Username)
			}
			seen[u.ID] = true
			if prevName != "" && u.Username <= prevName {
				t.Fatalf("out of order: %q after %q", u.Username, prevName)
			}
			prevName = u.Username
			count++
		}
		if !hasMore {
			break
		}
		last := page[len(page)-1]
		p.HasCursor = true
		p.AfterName = last.Username
		p.AfterID = last.ID
	}

	if count != total {
		t.Errorf("walked %d users across %d pages, want %d", count, pages, total)
	}
	if pages < 3 {
		t.Errorf("expected at least 3 pages for %d users, got %d", total, pages)
	}
}
