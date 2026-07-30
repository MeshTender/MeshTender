package core

import (
	"net/http"
	"net/url"
	"sort"
	"testing"
	"time"

	"github.com/jleight/meshtender/internal/auth"
)

// TestVerifyPasswordSpendsEqualTime is the test that actually guards the fix: it
// calls Service.VerifyPassword directly and measures it, so deleting the
// spendPasswordWork calls fails here.
//
// The unit tests in internal/auth pin the building blocks (the dummy hash's cost,
// and that comparePassword burns two bcrypt operations either way), but none of them
// notice if VerifyPassword simply stops calling it — which is the realistic
// regression. Going through the Service rather than the HTTP endpoint sidesteps the
// login rate limiter (burst 10), leaving room for repeated samples.
//
// Assertions are one-sided and calibrated against the "wrong password" case measured
// on the same machine, so there are no hardcoded durations. The regression this
// catches is enormous — an early return drops ~90ms to well under 1ms — so a loose
// floor is both safe and sufficient.
func TestVerifyPasswordSpendsEqualTime(t *testing.T) {
	// No t.Parallel: this measures wall-clock time.
	st, ctx := coreStore(t)

	svc, err := auth.New(st, st.Pool(), auth.Config{
		RPID: "localhost", RPDisplayName: "test",
		RPOrigins: []string{"http://auth.localhost"},
		AppHost:   testAppHost, AuthHost: testAuthHost, RootHost: testRootHost,
	})
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}

	withPassword, err := st.CreateUser(ctx, "timing-haspassword", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := svc.SetPassword(ctx, withPassword.ID, "the real password"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if _, err := st.CreateUser(ctx, "timing-passkeyonly", ""); err != nil {
		t.Fatalf("create user: %v", err)
	}

	const samples = 3
	median := func(username string) time.Duration {
		times := make([]time.Duration, samples)
		for i := range times {
			start := time.Now()
			if _, err := svc.VerifyPassword(ctx, username, "not the real password"); err == nil {
				t.Fatalf("VerifyPassword(%q) unexpectedly succeeded", username)
			}
			times[i] = time.Since(start)
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		return times[samples/2]
	}

	// The reference: a real account, wrong password — a full comparison.
	reference := median("timing-haspassword")
	if reference < 100*time.Microsecond {
		t.Skipf("bcrypt comparison too fast to measure reliably (%v)", reference)
	}

	for _, c := range []struct{ name, username string }{
		{"no such user", "timing-nosuchuser"},
		{"passkey-only account", "timing-passkeyonly"},
	} {
		got := median(c.username)
		ratio := float64(got) / float64(reference)
		if ratio < 0.5 {
			t.Errorf("%s took %v vs %v for a real wrong-password check (%.3fx) — "+
				"response time reveals the account's state", c.name, got, reference, ratio)
		}
		t.Logf("%-22s %v (%.2fx the reference %v)", c.name, got, ratio, reference)
	}
}

// TestLoginDoesNotRevealAccountState checks that the three ways a password sign-in
// can fail are indistinguishable in the response itself:
//
//  1. no such username
//  2. the username exists and has a password, but the password is wrong
//  3. the username exists but has no password at all (passkey-only)
//
// All three must produce the same status and the same redirect, so neither the
// status code nor the flash message discloses whether an account exists or which
// credentials it holds.
//
// The companion property — that all three also take the same amount of *time* — is
// pinned in internal/auth (TestSpendPasswordWorkDoesTwoComparisons proves the equal
// bcrypt work deterministically). It isn't measured here because the endpoint is
// rate-limited to a burst of 10, so there's no room for repeated timing samples.
//
// Regression for audit finding S2.
func TestLoginDoesNotRevealAccountState(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	// Case 2's account: created through the real signup endpoint, so its stored hash
	// uses the app's actual scheme rather than one reconstructed in the test.
	signup := post(t, ts, h.auth, "/signup/password", url.Values{
		"username": {"haspassword"},
		"password": {"the real password"},
	})
	signup.Body.Close()
	if signup.StatusCode != http.StatusSeeOther {
		t.Fatalf("signup: status = %d, want 303", signup.StatusCode)
	}

	// Case 3's account: exists, but no password — sign-in must not reveal that
	// either (it would tell an attacker to switch to a passkey-phishing approach).
	if _, err := st.CreateUser(ctx, "passkeyonly", ""); err != nil {
		t.Fatalf("create user: %v", err)
	}

	type outcome struct {
		status   int
		location string
	}
	attempt := func(username, password string) outcome {
		t.Helper()
		resp := post(t, ts, h.auth, "/login/password", url.Values{
			"username": {username},
			"password": {password},
		})
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatal("hit the login rate limiter — this test must stay under the burst")
		}
		return outcome{resp.StatusCode, resp.Header.Get("Location")}
	}

	cases := []struct {
		name, username, password string
	}{
		{"no such user", "nosuchuser", "some password"},
		{"wrong password", "haspassword", "not the real password"},
		{"passkey-only account", "passkeyonly", "some password"},
	}

	var first outcome
	for i, c := range cases {
		got := attempt(c.username, c.password)
		if got.status != http.StatusSeeOther {
			t.Errorf("%s: status = %d, want 303", c.name, got.status)
		}
		if got.location == "" {
			t.Errorf("%s: no Location header on the failure redirect", c.name)
		}
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Errorf("%s is distinguishable from %q:\n  got  %+v\n  want %+v",
				c.name, cases[0].name, got, first)
		}
	}
}
