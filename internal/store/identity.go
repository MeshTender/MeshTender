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

// RestoreOutcome is what a restore attempt did.
type RestoreOutcome int

const (
	// RestoreAlreadyCurrent: the backup holds the identity this server already uses,
	// so nothing was written. Re-pasting the same backup is safe and idempotent.
	RestoreAlreadyCurrent RestoreOutcome = iota
	// RestoreInstalled: the identity was replaced.
	RestoreInstalled
	// RestoreRefusedInUse: the backup holds a DIFFERENT identity and this server has
	// repeaters registered against the current one. Installing it would leave every
	// one of those repeaters with an ACL entry for a key MeshTender no longer holds —
	// unadministrable until each owner physically re-granted access.
	RestoreRefusedInUse
)

// ReplaceServerIdentityIfUnused installs a restored identity, refusing when doing so
// would orphan repeaters in the field.
//
// The guard is "are there any repeaters?" rather than "is there any identity?", because
// the latter is unreachable: LoadOrCreate mints an identity on boot whenever the row is
// absent, so by the time an operator can sign in to restore, a throwaway key is already
// stored. The real recovery case — database lost, master key and backup survive — has a
// freshly-generated identity and zero repeaters, which this permits. A database restored
// from backup instead has BOTH the right identity and its repeaters, where the backup is
// already current and this is a no-op.
//
// The count and the write share one transaction so a repeater registered concurrently
// (the deployment runs multiple replicas) can't slip past the guard.
func (s *Store) ReplaceServerIdentityIfUnused(ctx context.Context, pubKeyHex string, sealedSeed []byte) (RestoreOutcome, error) {
	outcome := RestoreRefusedInUse
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var current string
		err := tx.QueryRow(ctx,
			`SELECT public_key_hex FROM server_identity WHERE id = 1 FOR UPDATE`).Scan(&current)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// No identity at all (a restore racing first boot). Install it.
			if _, err := tx.Exec(ctx,
				`INSERT INTO server_identity (id, public_key_hex, encrypted_seed) VALUES (1, $1, $2)
				 ON CONFLICT (id) DO UPDATE SET public_key_hex = $1, encrypted_seed = $2`,
				pubKeyHex, sealedSeed); err != nil {
				return fmt.Errorf("insert restored identity: %w", err)
			}
			outcome = RestoreInstalled
			return nil
		case err != nil:
			return fmt.Errorf("read current identity: %w", err)
		}

		if current == pubKeyHex {
			outcome = RestoreAlreadyCurrent
			return nil
		}
		var repeaters int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM repeaters`).Scan(&repeaters); err != nil {
			return fmt.Errorf("count repeaters: %w", err)
		}
		if repeaters > 0 {
			outcome = RestoreRefusedInUse
			return nil
		}
		if _, err := tx.Exec(ctx,
			`UPDATE server_identity SET public_key_hex = $1, encrypted_seed = $2 WHERE id = 1`,
			pubKeyHex, sealedSeed); err != nil {
			return fmt.Errorf("replace identity: %w", err)
		}
		outcome = RestoreInstalled
		return nil
	})
	if err != nil {
		return RestoreRefusedInUse, err
	}
	return outcome, nil
}
