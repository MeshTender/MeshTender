// Package auth provides account registration, passkey (WebAuthn) and password
// authentication, and session management for MeshTender.
package auth

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/MeshTender/MeshTender/internal/store"
)

// webauthnUser adapts a store.User plus its credentials to webauthn.User.
type webauthnUser struct {
	user  *store.User
	creds []webauthn.Credential
}

func (u *webauthnUser) WebAuthnID() []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(u.user.ID)) //nolint:gosec // G115: user row ID is non-negative
	return b
}

func (u *webauthnUser) WebAuthnName() string                       { return u.user.Username }
func (u *webauthnUser) WebAuthnDisplayName() string                { return u.user.Name() }
func (u *webauthnUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// loadWebAuthnUser builds a webauthn.User by loading and unmarshaling the
// user's stored credentials.
func (s *Service) loadWebAuthnUser(ctx context.Context, u *store.User) (*webauthnUser, error) {
	blobs, err := s.store.GetCredentials(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	creds := make([]webauthn.Credential, 0, len(blobs))
	for _, b := range blobs {
		var c webauthn.Credential
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, fmt.Errorf("unmarshal credential: %w", err)
		}
		creds = append(creds, c)
	}
	return &webauthnUser{user: u, creds: creds}, nil
}
