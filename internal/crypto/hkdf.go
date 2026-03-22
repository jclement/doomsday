package crypto

import (
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// HKDF domain labels for sub-key derivation.
// Each label produces a cryptographically independent sub-key.
const (
	domainData      = "doomsday-data-v1"
	domainTree      = "doomsday-tree-v1"
	domainIndex     = "doomsday-index-v1"
	domainSnapshot  = "doomsday-snapshot-v1"
	domainConfig    = "doomsday-config-v1"
	domainContentID = "doomsday-content-id"
)

// DeriveSubKeys derives all sub-keys from a master key using HKDF-SHA256.
// Each sub-key is domain-separated, making cross-context ciphertext substitution impossible.
func DeriveSubKeys(master MasterKey) (*SubKeys, error) {
	sk := &SubKeys{}
	derivations := []struct {
		label string
		dest  *[32]byte
	}{
		{domainData, &sk.Data},
		{domainTree, &sk.Tree},
		{domainIndex, &sk.Index},
		{domainSnapshot, &sk.Snapshot},
		{domainConfig, &sk.Config},
		{domainContentID, &sk.ContentID},
	}
	for _, d := range derivations {
		if err := deriveKey(master[:], d.label, d.dest); err != nil {
			sk.Zero()
			return nil, fmt.Errorf("crypto.DeriveSubKeys: %w", err)
		}
	}
	return sk, nil
}

// deriveKey uses HKDF-SHA256 to derive a single 32-byte key.
// salt is nil (HKDF uses a zero salt internally when nil).
func deriveKey(ikm []byte, info string, out *[32]byte) error {
	r := hkdf.New(sha256.New, ikm, nil, []byte(info))
	if _, err := io.ReadFull(r, out[:]); err != nil {
		return fmt.Errorf("hkdf derive %q: %w", info, err)
	}
	return nil
}
