package identity

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	t.Parallel()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("a 32-byte ed25519 seed goes here")

	sealed, err := seal(key, plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("sealed blob contains plaintext")
	}

	got, err := open(key, sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip mismatch: got %q want %q", got, plaintext)
	}
}

func TestOpenWrongKeyFails(t *testing.T) {
	t.Parallel()
	var key, wrong [32]byte
	_, _ = rand.Read(key[:])
	_, _ = rand.Read(wrong[:])

	sealed, err := seal(key, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := open(wrong, sealed); err == nil {
		t.Fatal("expected open with wrong key to fail")
	}
}

func TestOpenTooShort(t *testing.T) {
	t.Parallel()
	var key [32]byte
	_, _ = rand.Read(key[:])
	if _, err := open(key, []byte{0x01, 0x02}); err == nil {
		t.Fatal("expected error on short input")
	}
}
