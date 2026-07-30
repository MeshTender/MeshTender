package identity

import (
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// newSealedIdentity produces a fresh identity sealed under key, mimicking what's in
// the server_identity row.
func newSealedIdentity(t *testing.T, key [32]byte) (pubHex string, sealedSeed []byte) {
	t.Helper()
	local, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	seed := local.Seed()
	sealed, err := seal(key, seed[:])
	if err != nil {
		t.Fatalf("seal seed: %v", err)
	}
	return local.String(), sealed
}

func testKey(t *testing.T, fill byte) [32]byte {
	t.Helper()
	var k [32]byte
	for i := range k {
		k[i] = fill
	}
	return k
}

// TestBackupRoundTrip is the property the whole feature rests on: what export produces,
// restore can open, and it yields the same identity.
func TestBackupRoundTrip(t *testing.T) {
	t.Parallel()
	key := testKey(t, 0x11)
	pub, sealed := newSealedIdentity(t, key)

	envelope, err := ExportBackup(key, pub, sealed)
	if err != nil {
		t.Fatalf("ExportBackup: %v", err)
	}
	if !strings.HasPrefix(envelope, backupVersion+".") {
		t.Errorf("envelope lacks its version prefix: %q", envelope)
	}
	// The public key must be readable without decrypting, so an operator can tell which
	// identity a password-manager entry holds at a glance.
	if !strings.Contains(envelope, pub) {
		t.Error("envelope doesn't carry the public key in the clear")
	}
	// The seed must NOT be readable.
	if strings.Contains(envelope, "seed") {
		t.Error("envelope leaks something seed-shaped in the clear")
	}

	got, err := ParseBackup(envelope)
	if err != nil {
		t.Fatalf("ParseBackup: %v", err)
	}
	if got.PublicKeyHex != pub {
		t.Errorf("parsed public key = %q, want %q", got.PublicKeyHex, pub)
	}
	// Byte-identical to the stored column, so a backup and its row are comparable.
	if string(got.SealedSeed) != string(sealed) {
		t.Error("parsed sealed seed differs from the stored one")
	}

	local, err := got.Open(key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if local.String() != pub {
		t.Errorf("opened identity = %s, want %s", local.String(), pub)
	}
}

// TestBackupRejectsWrongMasterKey: a backup is only useful to someone holding the same
// master key. This is what makes the envelope safe to store anywhere.
func TestBackupRejectsWrongMasterKey(t *testing.T) {
	t.Parallel()
	right, wrong := testKey(t, 0x22), testKey(t, 0x33)
	pub, sealed := newSealedIdentity(t, right)

	envelope, err := ExportBackup(right, pub, sealed)
	if err != nil {
		t.Fatalf("ExportBackup: %v", err)
	}
	parsed, err := ParseBackup(envelope)
	if err != nil {
		t.Fatalf("ParseBackup: %v", err)
	}
	if _, err := parsed.Open(wrong); err == nil {
		t.Fatal("a backup opened under the WRONG master key — the ciphertext is not protecting the seed")
	}
}

// TestBackupRejectsBadInput covers every way a paste can go wrong, since these are what
// an operator will actually hit under stress during a recovery.
func TestBackupRejectsBadInput(t *testing.T) {
	t.Parallel()
	key := testKey(t, 0x44)
	pub, sealed := newSealedIdentity(t, key)
	good, err := ExportBackup(key, pub, sealed)
	if err != nil {
		t.Fatalf("ExportBackup: %v", err)
	}

	for _, tc := range []struct{ name, input string }{
		{"empty", ""},
		{"unrelated text", "hello world"},
		{"wrong version", strings.Replace(good, backupVersion, "meshtender-identity-v9", 1)},
		{"missing payload", backupVersion + "." + pub},
		{"extra section", good + ".extra"},
		{"non-hex public key", backupVersion + ".nothex." + strings.Split(good, ".")[2]},
		{"payload not base64", backupVersion + "." + pub + ".!!!not-base64!!!"},
	} {
		if _, err := ParseBackup(tc.input); !errors.Is(err, ErrBackupFormat) {
			t.Errorf("%s: err = %v, want ErrBackupFormat", tc.name, err)
		}
	}

	// Surrounding whitespace is what you get from a copy/paste, so it must be tolerated.
	if _, err := ParseBackup("  \n" + good + "\n  "); err != nil {
		t.Errorf("a backup with surrounding whitespace was rejected: %v", err)
	}
}

// TestBackupRejectsTruncatedPayload: a partial copy out of a password manager must fail
// loudly rather than restore something wrong.
func TestBackupRejectsTruncatedPayload(t *testing.T) {
	t.Parallel()
	key := testKey(t, 0x55)
	pub, sealed := newSealedIdentity(t, key)
	good, err := ExportBackup(key, pub, sealed)
	if err != nil {
		t.Fatalf("ExportBackup: %v", err)
	}
	parts := strings.Split(good, ".")
	truncated := parts[0] + "." + parts[1] + "." + parts[2][:len(parts[2])-8]

	parsed, err := ParseBackup(truncated)
	if err != nil {
		return // rejected at parse time, which is fine
	}
	if _, err := parsed.Open(key); err == nil {
		t.Fatal("a truncated payload opened successfully")
	}
}

// TestBackupRejectsRelabelledEnvelope is the reason Open cross-checks the public key.
// GCM authenticates the ciphertext, but the label sits OUTSIDE it — so an envelope
// edited to claim a different identity than it holds must be caught, or a restore
// could install one identity while reporting another.
func TestBackupRejectsRelabelledEnvelope(t *testing.T) {
	t.Parallel()
	key := testKey(t, 0x66)
	pubA, sealedA := newSealedIdentity(t, key)
	pubB, _ := newSealedIdentity(t, key)

	good, err := ExportBackup(key, pubA, sealedA)
	if err != nil {
		t.Fatalf("ExportBackup: %v", err)
	}
	// Swap the plaintext label for a different, valid public key.
	relabelled := strings.Replace(good, pubA, pubB, 1)

	parsed, err := ParseBackup(relabelled)
	if err != nil {
		t.Fatalf("ParseBackup: %v", err)
	}
	if _, err := parsed.Open(key); err == nil {
		t.Fatal("an envelope whose label was swapped for another identity opened without complaint")
	}
}

// TestExportRefusesUnverifiableIdentity: export must not hand out a backup that
// wouldn't restore. That failure would otherwise stay hidden until the day it's needed.
func TestExportRefusesUnverifiableIdentity(t *testing.T) {
	t.Parallel()
	key := testKey(t, 0x77)
	pubA, sealedA := newSealedIdentity(t, key)
	pubB, _ := newSealedIdentity(t, key)

	// Stored public key doesn't match the sealed seed (a corrupt row).
	if _, err := ExportBackup(key, pubB, sealedA); err == nil {
		t.Error("exported a backup whose seed doesn't derive the stored public key")
	}
	// Sealed seed can't be opened at all (wrong key / corrupt bytes).
	if _, err := ExportBackup(testKey(t, 0x88), pubA, sealedA); err == nil {
		t.Error("exported a backup that can't be decrypted")
	}
	// Right length but not a sealed seed.
	if _, err := ExportBackup(key, pubA, []byte("clearly not sealed")); err == nil {
		t.Error("exported a backup from garbage bytes")
	}
}
