package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/observe"
)

// TestUserObservers: a member adds an observer by key (once, up to the cap);
// its status is the most recently heard organization's report, it counts every
// organization that lists it, and only its owner can see or remove it.
func TestUserObservers(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	alice, err := st.CreateUser(ctx, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateUser(ctx, "bob", "")
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("ab", 32)

	o, err := st.CreateUserObserver(ctx, alice.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	if o.Reported() || o.Name != "" || o.OrgCount != 0 {
		t.Errorf("a new observer no organization lists: %+v", o)
	}
	if _, err := st.CreateUserObserver(ctx, alice.ID, key); !errors.Is(err, ErrDuplicate) {
		t.Errorf("adding it twice: err = %v, want ErrDuplicate", err)
	}
	if _, err := st.CreateUserObserver(ctx, bob.ID, key); err != nil {
		t.Errorf("another member adding the same observer: %v", err)
	}

	// Two organizations hear it; the later report is the one shown.
	early, err := st.CreateOrg(ctx, "Early Org", bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	late, err := st.CreateOrg(ctx, "Late Org", bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := st.ReportObserver(ctx, early.ID, key, "SEA", now.Add(-time.Hour), &observe.Status{Name: "Old Name", FirmwareVersion: "v1.17"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReportObserver(ctx, late.ID, key, "PDX", now, &observe.Status{Name: "Hilltop", FirmwareVersion: "v1.18.0.4313-meshtender-fe351d9f", Model: "Heltec V4"}); err != nil {
		t.Fatal(err)
	}

	list, err := st.ListObserversForUser(ctx, alice.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	got := list[0]
	if got.Name != "Hilltop" || got.IATA != "PDX" || got.Model != "Heltec V4" || got.OrgCount != 2 || !got.Reported() ||
		!strings.Contains(got.FirmwareVersion, "4313") {
		t.Errorf("listed observer = %+v, want the Late Org report and a count of 2", got)
	}

	// Alice is only in Late Org: that one is marked as hers.
	if err := st.AddOrgMember(ctx, late.ID, alice.ID, "member"); err != nil {
		t.Fatal(err)
	}
	reports, err := st.ObserverReports(ctx, key, alice.ID)
	if err != nil || len(reports) != 2 {
		t.Fatalf("reports = %v, %v", reports, err)
	}
	if reports[0].OrgName != "Late Org" || !reports[0].Member || reports[1].Member || reports[0].Approved() {
		t.Errorf("reports = %+v, want Late Org first, hers, and unapproved", reports)
	}

	if _, err := st.GetUserObserver(ctx, bob.ID, o.PublicID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another member getting alice's record: err = %v, want ErrNotFound", err)
	}
	if err := st.DeleteUserObserver(ctx, bob.ID, o.PublicID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another member deleting alice's record: err = %v, want ErrNotFound", err)
	}
	if err := st.DeleteUserObserver(ctx, alice.ID, o.PublicID); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.ListObserversForUser(ctx, alice.ID); len(list) != 0 {
		t.Errorf("after delete, list = %v", list)
	}
}

func TestUserObserverLimit(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	u, err := st.CreateUser(ctx, "many", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range MaxObserversPerUser {
		if _, err := st.CreateUserObserver(ctx, u.ID, fmt.Sprintf("%064x", i+1)); err != nil {
			t.Fatalf("observer %d: %v", i, err)
		}
	}
	if _, err := st.CreateUserObserver(ctx, u.ID, fmt.Sprintf("%064x", MaxObserversPerUser+1)); !errors.Is(err, ErrObserverLimit) {
		t.Errorf("one past the cap: err = %v, want ErrObserverLimit", err)
	}
}
