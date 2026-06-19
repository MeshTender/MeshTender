package identity

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/jleight/meshtender/internal/store"
)

// Service holds the loaded server-wide MeshCore identity.
type Service struct {
	local meshcore.LocalIdentity
}

// Local returns the loaded server identity for crypto operations.
func (s *Service) Local() meshcore.LocalIdentity { return s.local }

// PublicKeyHex returns the server identity's public key as 64 hex chars. This
// is the value users embed in their `setperm <pubkey> 3` command.
func (s *Service) PublicKeyHex() string { return s.local.String() }

// SetPermCommand returns the repeater CLI command that grants this server
// identity admin (permission level 3) on a repeater.
func (s *Service) SetPermCommand() string {
	return fmt.Sprintf("setperm %s 3", s.local.String())
}

// LoadOrCreate loads the singleton server identity from the store, decrypting
// its seed with masterKey. If none exists, it generates a fresh identity,
// seals the seed, and persists it.
func LoadOrCreate(ctx context.Context, st *store.Store, masterKey [32]byte) (*Service, error) {
	pubHex, encSeed, err := st.GetServerIdentity(ctx)
	switch {
	case err == nil:
		seed, err := open(masterKey, encSeed)
		if err != nil {
			return nil, fmt.Errorf("decrypt server seed: %w", err)
		}
		var seedArr [32]byte
		if len(seed) != len(seedArr) {
			return nil, fmt.Errorf("server seed wrong length: got %d", len(seed))
		}
		copy(seedArr[:], seed)
		local := meshcore.NewLocalIdentityFromSeed(seedArr)
		if local.String() != pubHex {
			return nil, fmt.Errorf("server identity mismatch: decrypted seed yields %s, stored %s", local.String(), pubHex)
		}
		return &Service{local: local}, nil

	case errors.Is(err, store.ErrNoIdentity):
		return create(ctx, st, masterKey)

	default:
		return nil, err
	}
}

func create(ctx context.Context, st *store.Store, masterKey [32]byte) (*Service, error) {
	local, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate identity: %w", err)
	}
	seed := local.Seed()
	sealed, err := seal(masterKey, seed[:])
	if err != nil {
		return nil, fmt.Errorf("seal seed: %w", err)
	}
	if err := st.InsertServerIdentity(ctx, local.String(), sealed); err != nil {
		return nil, err
	}
	return &Service{local: local}, nil
}
