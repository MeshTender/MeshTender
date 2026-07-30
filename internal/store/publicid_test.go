package store

import (
	"strings"
	"testing"
)

// TestMaxUnbiasedByteIsLargestMultiple is the deterministic half of the fix: it pins
// the rejection threshold to the largest multiple of the alphabet size that fits in a
// byte. Get this wrong in either direction and the generator silently skews —
// too high reintroduces the bias, too low throws away entropy for nothing.
//
// No randomness involved, so it can't flake.
func TestMaxUnbiasedByteIsLargestMultiple(t *testing.T) {
	t.Parallel()
	n := len(base62)

	if int(maxUnbiasedByte)%n != 0 {
		t.Errorf("maxUnbiasedByte = %d is not a multiple of the alphabet size %d, so "+
			"%% %d still folds unevenly", maxUnbiasedByte, n, n)
	}
	// Largest such multiple: adding one more alphabet's worth must overflow a byte.
	if int(maxUnbiasedByte)+n <= 256 {
		t.Errorf("maxUnbiasedByte = %d is not the LARGEST multiple of %d under 256 "+
			"(%d would also fit) — rejecting more bytes than necessary",
			maxUnbiasedByte, n, int(maxUnbiasedByte)+n)
	}
	if int(maxUnbiasedByte) > 256 {
		t.Errorf("maxUnbiasedByte = %d exceeds the byte range", maxUnbiasedByte)
	}
}

// TestRandomPublicIDShape covers the basics: right length, only alphabet characters,
// and no repeats across a large batch.
func TestRandomPublicIDShape(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool, 5000)
	for i := 0; i < 5000; i++ {
		id, err := randomPublicID()
		if err != nil {
			t.Fatalf("randomPublicID: %v", err)
		}
		if len(id) != publicIDLen {
			t.Fatalf("id %q has length %d, want %d", id, len(id), publicIDLen)
		}
		for _, r := range id {
			if !strings.ContainsRune(base62, r) {
				t.Fatalf("id %q contains %q, which is outside the alphabet", id, r)
			}
		}
		if seen[id] {
			t.Fatalf("duplicate id %q after %d draws", id, i)
		}
		seen[id] = true
	}
}

// TestRandomPublicIDIsUnbiased is the statistical half: it samples enough characters
// that the old modulo fold would stand out unmistakably.
//
// Under the old code, '0'–'7' each landed 5 times per 256 byte values against 4 for
// the other 54 characters — 25% more often. The tolerance below is set between the
// two regimes: the fixed generator stays inside it with overwhelming probability
// (worst-case deviation across 62 buckets is a few sigma, well under 15%), while the
// biased one is pinned near +21% and fails every time. That gap is what keeps this
// from flaking in either direction.
func TestRandomPublicIDIsUnbiased(t *testing.T) {
	t.Parallel()
	const (
		draws     = 12000               // ids
		tolerance = 0.15                // fractional deviation from the mean
		total     = draws * publicIDLen // characters observed
	)

	counts := make(map[rune]int, len(base62))
	for i := 0; i < draws; i++ {
		id, err := randomPublicID()
		if err != nil {
			t.Fatalf("randomPublicID: %v", err)
		}
		for _, r := range id {
			counts[r]++
		}
	}

	if len(counts) != len(base62) {
		t.Errorf("only %d of %d alphabet characters ever appeared", len(counts), len(base62))
	}
	expected := float64(total) / float64(len(base62))
	worst, worstChar := 0.0, ' '
	for _, r := range base62 {
		dev := (float64(counts[r]) - expected) / expected
		if dev < 0 {
			dev = -dev
		}
		if dev > worst {
			worst, worstChar = dev, r
		}
	}
	if worst > tolerance {
		t.Errorf("character %q deviates %.1f%% from the expected %.0f occurrences "+
			"(tolerance %.0f%%) — the distribution is skewed, which is what modulo "+
			"folding of a non-multiple byte range produces",
			worstChar, worst*100, expected, tolerance*100)
	}
	t.Logf("%d characters over %d ids; worst deviation %.2f%% (%q), expected %.0f each",
		total, draws, worst*100, worstChar, expected)
}
