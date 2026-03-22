// Package crypto provides encryption, key derivation, and key management for doomsday.
// Uses Go stdlib crypto + golang.org/x/crypto. Never rolls custom primitives.
package crypto

// MasterKey is the 256-bit root key from which all sub-keys are derived.
type MasterKey [32]byte

// Zero overwrites the key material with zeros.
func (k *MasterKey) Zero() {
	for i := range k {
		k[i] = 0
	}
}

// SubKeys holds all domain-separated sub-keys derived from a master key via HKDF.
type SubKeys struct {
	Data      [32]byte // Encrypts data blobs
	Tree      [32]byte // Encrypts tree blobs
	Index     [32]byte // Encrypts index files
	Snapshot  [32]byte // Encrypts snapshot files
	Config    [32]byte // Encrypts repo config
	ContentID [32]byte // HMAC-SHA256 key for blob content IDs
}

// Zero overwrites all sub-key material.
func (s *SubKeys) Zero() {
	for i := range s.Data {
		s.Data[i] = 0
	}
	for i := range s.Tree {
		s.Tree[i] = 0
	}
	for i := range s.Index {
		s.Index[i] = 0
	}
	for i := range s.Snapshot {
		s.Snapshot[i] = 0
	}
	for i := range s.Config {
		s.Config[i] = 0
	}
	for i := range s.ContentID {
		s.ContentID[i] = 0
	}
}

// KeySet bundles a master key and its derived sub-keys for convenient passing.
type KeySet struct {
	Master  MasterKey
	SubKeys SubKeys
}

// Zero overwrites all key material.
func (ks *KeySet) Zero() {
	ks.Master.Zero()
	ks.SubKeys.Zero()
}
