package store

import (
	"context"
	"testing"
	"time"
)

// collectUsers walks every page for the given params and returns the flattened
// list (exercising the keyset cursor across pages).
func collectUsers(t *testing.T, st *Store, ctx context.Context, p UserListParams) []*User {
	t.Helper()
	var all []*User
	for {
		page, more, err := st.ListUsersPage(ctx, p)
		if err != nil {
			t.Fatalf("list users: %v", err)
		}
		all = append(all, page...)
		if !more {
			return all
		}
		last := page[len(page)-1]
		p.HasCursor = true
		p.AfterName = last.Username
		p.AfterID = last.ID
		switch p.Sort {
		case UserSortLastLogin:
			p.AfterTime = last.LastLoginKey()
		case UserSortNewest:
			p.AfterTime = last.CreatedAt
		}
	}
}

func TestListUsersFilterSearchSort(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	// The first account created bootstraps both manage caps; make it a throwaway
	// so the accounts under test have exactly the caps we set.
	if _, err := st.CreateUser(ctx, "zzz-root", ""); err != nil {
		t.Fatalf("bootstrap user: %v", err)
	}
	mk := func(name string, manageUsers, manageCatalog bool) *User {
		u, err := st.CreateUser(ctx, name, name+" display")
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if manageUsers || manageCatalog {
			if err := st.SetCapabilities(ctx, u.ID, manageUsers, manageCatalog); err != nil {
				t.Fatalf("caps %s: %v", name, err)
			}
		}
		return u
	}
	alice := mk("alice", true, false)
	bob := mk("bob", false, true)
	carol := mk("carol", false, false)

	// Capability filter: zzz-root has both caps (bootstrap), so it counts under
	// both managers and catalog.
	if got := collectUsers(t, st, ctx, UserListParams{Cap: UserCapManagers}); len(got) != 2 {
		t.Fatalf("managers filter = %d users, want 2 (zzz-root, alice)", len(got))
	}
	if got := collectUsers(t, st, ctx, UserListParams{Cap: UserCapCatalog}); len(got) != 2 {
		t.Fatalf("catalog filter = %d users, want 2 (zzz-root, bob)", len(got))
	}
	none := collectUsers(t, st, ctx, UserListParams{Cap: UserCapNone})
	if len(none) != 1 || none[0].ID != carol.ID {
		t.Fatalf("none filter = %v, want just carol", none)
	}

	// Search matches username or display name, case-insensitively.
	if got := collectUsers(t, st, ctx, UserListParams{Query: "ALI"}); len(got) != 1 || got[0].ID != alice.ID {
		t.Fatalf("search 'ALI' = %v, want just alice", got)
	}

	// Last-login sort: set explicit times; a never-logged-in account sorts last.
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if _, err := st.pool.Exec(ctx, `UPDATE users SET last_login_at=$2 WHERE id=$1`, bob.ID, base); err != nil {
		t.Fatalf("set bob login: %v", err)
	}
	if _, err := st.pool.Exec(ctx, `UPDATE users SET last_login_at=$2 WHERE id=$1`, alice.ID, base.Add(time.Hour)); err != nil {
		t.Fatalf("set alice login: %v", err)
	}
	// carol and zzz-root never logged in (NULL) → sort after the two that did.
	byLogin := collectUsers(t, st, ctx, UserListParams{Sort: UserSortLastLogin})
	if byLogin[0].ID != alice.ID || byLogin[1].ID != bob.ID {
		t.Fatalf("last-login order front = %q, %q; want alice, bob", byLogin[0].Username, byLogin[1].Username)
	}
	if byLogin[len(byLogin)-1].LastLoginAt != nil {
		t.Fatalf("last-login order should end on a never-logged-in account, got %q", byLogin[len(byLogin)-1].Username)
	}
}
