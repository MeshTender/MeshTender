package store

import (
	"testing"
	"time"
)

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

// TestSessionLockWakesOnBrokerChange: saving or removing a broker notifies the
// leader's connection, while the ingester's own status writes don't (they'd
// wake it every minute for nothing). A wait that times out must leave the
// connection, and so the lock, intact.
func TestSessionLockWakesOnBrokerChange(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin, err := st.CreateUser(ctx, "notifyadmin", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "NotifyOrg", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := st.TryAcquireSessionLock(ctx, 43)
	if err != nil || lock == nil {
		t.Fatalf("acquire = %v, %v", lock, err)
	}
	defer lock.Release(ctx)
	if err := lock.Listen(ctx, BrokersChangedChannel); err != nil {
		t.Fatal(err)
	}

	if woke, err := lock.Wait(ctx, 50*time.Millisecond); err != nil || woke {
		t.Fatalf("idle wait = %v, %v; want a quiet timeout", woke, err)
	}
	if err := lock.Alive(ctx); err != nil {
		t.Fatalf("lock connection broken by a timed-out wait: %v", err)
	}

	b := OrgBroker{OrgID: org.ID, URL: "wss://mqtt.example.org", TopicPrefix: "meshcore", Enabled: true}
	if err := st.UpsertOrgBroker(ctx, b, nil, false, admin.ID); err != nil {
		t.Fatal(err)
	}
	if woke, err := lock.Wait(ctx, 5*time.Second); err != nil || !woke {
		t.Fatalf("after save: woke=%v err=%v", woke, err)
	}

	now := time.Now()
	if err := st.RecordBrokerStatus(ctx, org.ID, &now, &now, ""); err != nil {
		t.Fatal(err)
	}
	if woke, _ := lock.Wait(ctx, 100*time.Millisecond); woke {
		t.Fatal("a status write woke the leader")
	}

	if err := st.DeleteOrgBroker(ctx, org.ID); err != nil {
		t.Fatal(err)
	}
	if woke, err := lock.Wait(ctx, 5*time.Second); err != nil || !woke {
		t.Fatalf("after delete: woke=%v err=%v", woke, err)
	}
}
