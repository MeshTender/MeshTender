package identity

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// The identity backup format is a self-describing envelope around the SAME sealed seed
// that's stored in the database:
//
//	meshtender-identity-v1.<public-key-hex>.<base64url(nonce||ciphertext||tag)>
//
// The ciphertext stays sealed under MESHTENDER_MASTER_KEY, which is what makes the
// whole string safe to paste into a password manager: it's useless to anyone who
// doesn't also hold the master key. Two consequences worth stating, because they're
// the reason this shape was chosen:
//
//   - Exporting is not a disclosure. An admin who exports gets ciphertext they can't
//     open, so the export needs no gate beyond the admin capability.
//   - Restoring is authenticated for free. Only someone holding the master key can
//     PRODUCE a valid envelope, so a hostile admin can't craft one containing an
//     identity they control. (A passphrase-sealed backup would be weaker here —
//     anyone could mint their own and restore it onto an empty database.)
//
// The plaintext parts carry no secrets: the public key is published in every advert
// and in the `setperm` command, and the version is a constant. They're outside the
// ciphertext on purpose, so a restore can identify and reject a bad paste before
// attempting any crypto, and so an operator can tell which identity a
// password-manager entry holds just by looking at it.
const (
	backupVersion = "meshtender-identity-v1"
	backupParts   = 3
)

// ErrBackupFormat means the pasted string isn't a MeshTender identity backup. It's
// distinct from a decryption failure so the UI can tell "you pasted the wrong thing"
// apart from "this doesn't match this server's master key".
var ErrBackupFormat = errors.New("identity: not a MeshTender identity backup")

// Backup is a parsed envelope. Parsing does no decryption, so holding one implies
// nothing about whether it opens under this server's master key.
type Backup struct {
	// PublicKeyHex is the identity the envelope CLAIMS to contain. It is not trusted
	// until Open cross-checks it against the seed inside the ciphertext.
	PublicKeyHex string
	// SealedSeed is the sealed seed, byte-identical to the database column, so a
	// backup and the row it came from are directly comparable.
	SealedSeed []byte
}

// ExportBackup builds a backup envelope from the stored identity.
//
// It verifies before returning anything: the sealed seed must open under masterKey,
// be the right length, and derive exactly pubKeyHex. Handing out a backup that
// wouldn't restore is the one failure mode that matters here, because it wouldn't be
// discovered until the day it's needed.
func ExportBackup(masterKey [32]byte, pubKeyHex string, sealedSeed []byte) (string, error) {
	if _, err := identityFromSealedSeed(masterKey, sealedSeed, pubKeyHex); err != nil {
		return "", fmt.Errorf("verify identity before export: %w", err)
	}
	return strings.Join([]string{
		backupVersion,
		pubKeyHex,
		base64.RawURLEncoding.EncodeToString(sealedSeed),
	}, "."), nil
}

// ParseBackup validates the envelope's shape and decodes it, WITHOUT decrypting. A
// wrong paste therefore fails here with a clear message rather than surfacing as an
// opaque crypto error.
func ParseBackup(s string) (Backup, error) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != backupParts || parts[0] != backupVersion {
		return Backup{}, ErrBackupFormat
	}
	pub := strings.ToLower(strings.TrimSpace(parts[1]))
	if _, err := meshcore.NewIdentityFromHex(pub); err != nil {
		return Backup{}, fmt.Errorf("%w: public key is not valid hex", ErrBackupFormat)
	}
	sealed, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Backup{}, fmt.Errorf("%w: payload is not valid base64url (truncated on paste?)", ErrBackupFormat)
	}
	return Backup{PublicKeyHex: pub, SealedSeed: sealed}, nil
}

// Open decrypts the envelope under masterKey and cross-checks that the seed inside
// derives the public key on the outside. GCM already authenticates the ciphertext;
// this additionally catches an envelope whose plaintext label was edited to claim a
// different identity than it holds.
func (b Backup) Open(masterKey [32]byte) (meshcore.LocalIdentity, error) {
	return identityFromSealedSeed(masterKey, b.SealedSeed, b.PublicKeyHex)
}

// identityFromSealedSeed opens a sealed seed and returns the identity it derives,
// requiring that identity to match wantPubKeyHex. Shared by export verification and
// restore so both apply the identical check.
func identityFromSealedSeed(masterKey [32]byte, sealedSeed []byte, wantPubKeyHex string) (meshcore.LocalIdentity, error) {
	var zero meshcore.LocalIdentity
	seed, err := open(masterKey, sealedSeed)
	if err != nil {
		// Deliberately vague about which part failed: the common cause is simply the
		// wrong master key, and GCM gives us no more detail than that anyway.
		return zero, fmt.Errorf("could not decrypt with this server's master key: %w", err)
	}
	var seedArr [32]byte
	if len(seed) != len(seedArr) {
		return zero, fmt.Errorf("seed is %d bytes, want %d", len(seed), len(seedArr))
	}
	copy(seedArr[:], seed)
	local := meshcore.NewLocalIdentityFromSeed(seedArr)
	// Constant-time compare so a mismatch can't be probed byte by byte. The values
	// aren't secret, but the comparison is free to do properly.
	if subtle.ConstantTimeCompare([]byte(local.String()), []byte(wantPubKeyHex)) != 1 {
		return zero, fmt.Errorf("seed derives %s but the backup claims %s", local.String(), wantPubKeyHex)
	}
	return local, nil
}
