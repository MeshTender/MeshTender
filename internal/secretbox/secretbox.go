// Package secretbox encrypts small secrets at rest under the server's master key
// (MESHTENDER_MASTER_KEY): the server identity seed, and org MQTT broker passwords.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

// Seal encrypts plaintext with AES-256-GCM under key, prefixing the random
// nonce. The result is self-describing: Open reads the nonce back off.
func Seal(key [32]byte, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	// Seal appends the ciphertext to nonce, so the returned slice is
	// nonce || ciphertext || tag.
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Open reverses Seal.
func Open(key [32]byte, sealed []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(sealed) < ns {
		return nil, fmt.Errorf("sealed data too short: %d < %d", len(sealed), ns)
	}
	nonce, ct := sealed[:ns], sealed[ns:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return pt, nil
}

func newGCM(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return gcm, nil
}
