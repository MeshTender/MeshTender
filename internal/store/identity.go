package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrNoIdentity is returned when the singleton server identity row is absent.
var ErrNoIdentity = errors.New("store: server identity not found")

// GetServerIdentity returns the stored public key (hex) and the encrypted
// seed blob for the singleton server identity, or ErrNoIdentity if none exists.
func (s *Store) GetServerIdentity(ctx context.Context) (pubKeyHex string, encryptedSeed []byte, err error) {
	row := s.pool.QueryRow(ctx,
		`SELECT public_key_hex, encrypted_seed FROM server_identity WHERE id = 1`)
	err = row.Scan(&pubKeyHex, &encryptedSeed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrNoIdentity
	}
	if err != nil {
		return "", nil, fmt.Errorf("get server identity: %w", err)
	}
	return pubKeyHex, encryptedSeed, nil
}

// InsertServerIdentity persists the singleton server identity. It fails if a
// row already exists (id is fixed at 1).
func (s *Store) InsertServerIdentity(ctx context.Context, pubKeyHex string, encryptedSeed []byte) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO server_identity (id, public_key_hex, encrypted_seed) VALUES (1, $1, $2)`,
		pubKeyHex, encryptedSeed)
	if err != nil {
		return fmt.Errorf("insert server identity: %w", err)
	}
	return nil
}
