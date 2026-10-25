package store

import "testing"

func TestSessionLockElectsOneHolder(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	const key = 42

	first, err := st.TryAcquireSessionLock(ctx, key)
	if err != nil || first == nil {
		t.Fatalf("first acquire = %v, %v", first, err)
	}
	if err := first.Alive(ctx); err != nil {
		t.Fatal(err)
	}
	// Another session can't take it while it's held…
	second, err := st.TryAcquireSessionLock(ctx, key)
	if err != nil || second != nil {
		t.Fatalf("second acquire = %v, %v; want nil, nil", second, err)
	}
	// …and can once it's released.
	first.Release(ctx)
	third, err := st.TryAcquireSessionLock(ctx, key)
	if err != nil || third == nil {
		t.Fatalf("after release = %v, %v", third, err)
	}
	third.Release(ctx)
}
