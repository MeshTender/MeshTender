package store

import (
	"fmt"
	"testing"
)

// TestListPublicOrgsPage walks the keyset-paginated directory and checks every
// org appears exactly once, in (name, id) order, across pages.
func TestListPublicOrgsPage(t *testing.T) {
	st, ctx := orgTestStore(t)

	owner, err := st.CreateUser(ctx, "pager", "")
	if err != nil {
		t.Fatal(err)
	}
	// Create more orgs than a single page so we exercise the seek across pages.
	total := OrgsPageSize*2 + 3
	for i := 0; i < total; i++ {
		// Zero-padded names give a deterministic lexical order to assert against.
		if _, err := st.CreateOrg(ctx, fmt.Sprintf("org-%04d", i), owner.ID); err != nil {
			t.Fatalf("create org %d: %v", i, err)
		}
	}

	var (
		seen      = map[int64]bool{}
		count     int
		afterName string
		afterID   int64
		prevName  string
		pages     int
	)
	for {
		page, hasMore, err := st.ListPublicOrgsPage(ctx, afterName, afterID)
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		pages++
		if len(page) > OrgsPageSize {
			t.Fatalf("page returned %d rows, exceeds OrgsPageSize %d", len(page), OrgsPageSize)
		}
		for _, o := range page {
			if seen[o.ID] {
				t.Fatalf("org %d (%q) returned twice", o.ID, o.Name)
			}
			seen[o.ID] = true
			if prevName != "" && o.Name < prevName {
				t.Fatalf("out of order: %q after %q", o.Name, prevName)
			}
			prevName = o.Name
			count++
		}
		if !hasMore {
			break
		}
		last := page[len(page)-1]
		afterName, afterID = last.Name, last.ID
	}

	if count != total {
		t.Errorf("walked %d orgs across %d pages, want %d", count, pages, total)
	}
	if pages < 3 {
		t.Errorf("expected at least 3 pages for %d orgs, got %d", total, pages)
	}
}
