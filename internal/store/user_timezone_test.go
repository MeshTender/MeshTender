package store

import "testing"

func TestSetTimezone(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	u, err := st.CreateUser(ctx, "tzuser", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// A fresh user has no zone set (auto-detect).
	if u.Timezone != "" {
		t.Fatalf("new user timezone = %q, want empty", u.Timezone)
	}

	// Setting a zone round-trips.
	if err := st.SetTimezone(ctx, u.ID, "America/New_York"); err != nil {
		t.Fatalf("set timezone: %v", err)
	}
	got, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if got.Timezone != "America/New_York" {
		t.Fatalf("timezone = %q, want America/New_York", got.Timezone)
	}

	// Clearing it returns to auto-detect.
	if err := st.SetTimezone(ctx, u.ID, ""); err != nil {
		t.Fatalf("clear timezone: %v", err)
	}
	got, err = st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if got.Timezone != "" {
		t.Fatalf("timezone = %q, want empty after clear", got.Timezone)
	}
}
