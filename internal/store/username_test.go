package store

import (
	"errors"
	"testing"
)

// selfChange is the metadata for a user renaming themselves.
func selfChange(uid int64) UsernameChangeContext {
	return UsernameChangeContext{ChangedBy: uid, IP: "203.0.113.7", UserAgent: "test-agent"}
}

func TestSetUsername(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	mk := func(name string) int64 {
		u, err := st.CreateUser(ctx, name, "")
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return u.ID
	}

	t.Run("rename records audit and updates user", func(t *testing.T) {
		uid := mk("alice")
		if err := st.SetUsername(ctx, uid, "alice2", selfChange(uid), true); err != nil {
			t.Fatalf("rename: %v", err)
		}
		u, err := st.GetUserByID(ctx, uid)
		if err != nil || u.Username != "alice2" {
			t.Fatalf("got %q (%v), want alice2", u.Username, err)
		}
		hist, err := st.ListUsernameChanges(ctx, uid, 10)
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if len(hist) != 1 {
			t.Fatalf("got %d history rows, want 1", len(hist))
		}
		h := hist[0]
		if h.OldUsername != "alice" || h.NewUsername != "alice2" || !h.BySelf {
			t.Fatalf("bad history row: %+v", h)
		}
		if h.IP == nil || *h.IP != "203.0.113.7" {
			t.Fatalf("ip not recorded: %+v", h.IP)
		}
	})

	t.Run("no-op rename writes no history", func(t *testing.T) {
		uid := mk("samename")
		if err := st.SetUsername(ctx, uid, "samename", selfChange(uid), true); err != nil {
			t.Fatalf("no-op: %v", err)
		}
		hist, err := st.ListUsernameChanges(ctx, uid, 10)
		if err != nil {
			t.Fatalf("list username changes: %v", err)
		}
		if len(hist) != 0 {
			t.Fatalf("no-op wrote %d history rows", len(hist))
		}
	})

	t.Run("duplicate of active user is rejected", func(t *testing.T) {
		a := mk("dupa")
		mk("dupb")
		if err := st.SetUsername(ctx, a, "dupb", selfChange(a), true); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("got %v, want ErrDuplicate", err)
		}
	})

	t.Run("rate limit blocks a second self-service rename", func(t *testing.T) {
		uid := mk("rl1")
		if err := st.SetUsername(ctx, uid, "rl2", selfChange(uid), true); err != nil {
			t.Fatalf("first rename: %v", err)
		}
		if err := st.SetUsername(ctx, uid, "rl3", selfChange(uid), true); !errors.Is(err, ErrRenameTooSoon) {
			t.Fatalf("got %v, want ErrRenameTooSoon", err)
		}
		// An admin-initiated change (enforceInterval=false) bypasses the limit.
		admin := mk("rladmin")
		if err := st.SetUsername(ctx, uid, "rl3", UsernameChangeContext{ChangedBy: admin}, false); err != nil {
			t.Fatalf("admin bypass: %v", err)
		}
		// NextRenameAllowed reflects the user's own recent change.
		next, err := st.NextRenameAllowed(ctx, uid)
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		if next == nil {
			t.Fatalf("expected a cooldown deadline, got nil")
		}
	})

	t.Run("released name is reserved from others but reclaimable by owner", func(t *testing.T) {
		owner := mk("shared")
		other := mk("other")
		// Owner releases "shared".
		if err := st.SetUsername(ctx, owner, "shared-new", selfChange(owner), true); err != nil {
			t.Fatalf("release: %v", err)
		}
		// Someone else can't take it during the cooldown.
		if err := st.SetUsername(ctx, other, "shared", selfChange(other), true); !errors.Is(err, ErrUsernameReserved) {
			t.Fatalf("got %v, want ErrUsernameReserved", err)
		}
		// Signup can't grab it either.
		if _, err := st.CreateUser(ctx, "shared", ""); !errors.Is(err, ErrUsernameReserved) {
			t.Fatalf("signup got %v, want ErrUsernameReserved", err)
		}
		// The original owner may reclaim it (rate limit aside, tested via the
		// admin path to isolate the cooldown rule).
		if err := st.SetUsername(ctx, owner, "shared", UsernameChangeContext{ChangedBy: owner}, false); err != nil {
			t.Fatalf("owner reclaim: %v", err)
		}
	})
}
