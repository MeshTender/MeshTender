package auth

import (
	"sort"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// TestDummyPasswordHashMatchesStoredHashes pins the two structural properties that
// make spendPasswordWork indistinguishable from a real verification: the dummy hash
// costs the same bcrypt rounds as a hash the app actually stores, and no password
// can accidentally match it.
//
// Cost is asserted against a *freshly produced* hash rather than the literal
// bcrypt.DefaultCost, so the two can never drift apart: if hashPassword ever moves
// off DefaultCost, the dummy follows it and this test keeps passing for the right
// reason.
func TestDummyPasswordHashMatchesStoredHashes(t *testing.T) {
	t.Parallel()

	real, err := hashPassword("a real user's password")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	realCost, err := bcrypt.Cost([]byte(real))
	if err != nil {
		t.Fatalf("bcrypt.Cost(real): %v", err)
	}
	dummyCost, err := bcrypt.Cost([]byte(dummyPasswordHash))
	if err != nil {
		t.Fatalf("bcrypt.Cost(dummy): %v — is it a valid bcrypt hash?", err)
	}
	if dummyCost != realCost {
		t.Errorf("dummy hash cost = %d, stored hash cost = %d; the work must match",
			dummyCost, realCost)
	}

	// Whatever a caller submits, it must not verify.
	for _, guess := range []string{"", "password", "a real user's password", dummyPasswordHash} {
		if ok, _ := comparePassword(dummyPasswordHash, guess); ok {
			t.Errorf("dummy hash matched %q — it must never verify", guess)
		}
	}
}

// TestSpendPasswordWorkDoesTwoComparisons proves — deterministically, with no
// timing involved — that the no-stored-hash path burns the same number of bcrypt
// operations as a real failed verification.
//
// The proof rests on comparePassword's return shape. It tries the pre-hash scheme
// first, then the legacy raw scheme, and its results are distinguishable:
//
//	(true,  false) → matched on the first try  → 1 bcrypt op
//	(true,  true)  → matched on the second try → 2 bcrypt ops
//	(false, false) → matched neither           → 2 bcrypt ops
//
// So observing (false, false) for both the dummy hash and a real hash with a wrong
// password establishes that both cost exactly two operations. That's the property
// that matters, and unlike a duration comparison it cannot flake.
func TestSpendPasswordWorkDoesTwoComparisons(t *testing.T) {
	t.Parallel()

	real, err := hashPassword("stored password")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}

	// The reference: an existing user typing the wrong password.
	ok, legacy := comparePassword(real, "wrong password")
	if ok || legacy {
		t.Fatalf("real hash + wrong password = (%v, %v), want (false, false)", ok, legacy)
	}

	// The path taken when there is no user, or the user has no password. Same shape
	// ⇒ same number of bcrypt operations.
	ok, legacy = comparePassword(dummyPasswordHash, "wrong password")
	if ok || legacy {
		t.Fatalf("dummy hash = (%v, %v), want (false, false) — the no-hash path must "+
			"try both schemes, like a real failed check", ok, legacy)
	}
}

// TestSpendPasswordWorkActuallyCostsTime is the belt-and-braces companion to the
// structural test above: it confirms real bcrypt work happens, catching a change
// that made spendPasswordWork a no-op (or stubbed comparePassword) while keeping
// its signature.
//
// Deliberately one-sided and calibrated. It measures a genuine failed comparison on
// this machine and requires the dummy path to cost a comparable amount, rather than
// hardcoding milliseconds that would be wrong on other hardware. The realistic
// regressions are dramatic — removing the call drops ~100ms to ~1µs (a factor of
// 10^5) — so a loose floor catches them with no flake risk. Medians damp scheduler
// noise.
func TestSpendPasswordWorkActuallyCostsTime(t *testing.T) {
	// No t.Parallel: this measures wall-clock, so it shouldn't share a core with the
	// package's other bcrypt-heavy tests.
	const samples = 5

	real, err := hashPassword("stored password")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}

	medianOf := func(f func()) time.Duration {
		times := make([]time.Duration, samples)
		for i := range times {
			start := time.Now()
			f()
			times[i] = time.Since(start)
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		return times[samples/2]
	}

	reference := medianOf(func() { _, _ = comparePassword(real, "wrong password") })
	dummy := medianOf(func() { spendPasswordWork("wrong password") })

	// Guard against a machine so fast the measurement is meaningless.
	if reference < 100*time.Microsecond {
		t.Skipf("bcrypt comparison too fast to measure reliably (%v)", reference)
	}
	if ratio := float64(dummy) / float64(reference); ratio < 0.5 {
		t.Errorf("spendPasswordWork took %v vs a real failed comparison's %v (%.3fx) — "+
			"the no-hash path isn't doing the work, so response time reveals whether "+
			"the account exists", dummy, reference, ratio)
	}
	t.Logf("real failed comparison %v, dummy path %v", reference, dummy)
}
