package crypto

import (
	"crypto/rand"
	"fmt"
	"io"

	"github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/scrypt"
)

// ScryptParams holds the scrypt key derivation parameters.
// Stored in each key file so parameters can be tuned per-key.
type ScryptParams struct {
	N int `json:"N"` // CPU/memory cost parameter (default 2^17 = 131072)
	R int `json:"r"` // Block size (default 8)
	P int `json:"p"` // Parallelization (default 1)
}

// DefaultScryptParams returns the default scrypt parameters.
// N=2^17, r=8, p=1 — ~128 MiB RAM, ~0.5s on modern hardware.
func DefaultScryptParams() ScryptParams {
	return ScryptParams{N: 1 << 17, R: 8, P: 1}
}

// DeriveFromPassword derives a 32-byte key encryption key (KEK) from a password using scrypt.
func DeriveFromPassword(password []byte, salt []byte, params ScryptParams) ([32]byte, error) {
	var kek [32]byte
	derived, err := scrypt.Key(password, salt, params.N, params.R, params.P, 32)
	if err != nil {
		return kek, fmt.Errorf("crypto.DeriveFromPassword: %w", err)
	}
	copy(kek[:], derived)
	zeroSlice(derived) // SECURITY: wipe scrypt output from heap
	return kek, nil
}

// GenerateSalt generates a 32-byte random salt for scrypt.
func GenerateSalt() ([]byte, error) {
	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("crypto.GenerateSalt: %w", err)
	}
	return salt, nil
}

// GenerateRecoveryPhrase generates a 24-word BIP39 mnemonic (256 bits of entropy)
// and derives a master key from it.
func GenerateRecoveryPhrase() (mnemonic string, master MasterKey, err error) {
	entropy, err := bip39.NewEntropy(256)
	if err != nil {
		return "", master, fmt.Errorf("crypto.GenerateRecoveryPhrase: %w", err)
	}
	defer zeroSlice(entropy) // SECURITY: wipe entropy from heap

	mnemonic, err = bip39.NewMnemonic(entropy)
	if err != nil {
		return "", master, fmt.Errorf("crypto.GenerateRecoveryPhrase: %w", err)
	}
	// Derive master key: use BIP39 seed (512-bit) truncated to 256-bit.
	// The mnemonic IS the key — no scrypt needed for recovery phrases.
	seed := bip39.NewSeed(mnemonic, "doomsday")
	copy(master[:], seed[:32])
	zeroSlice(seed) // SECURITY: wipe seed from heap
	return mnemonic, master, nil
}

// MasterKeyFromMnemonic re-derives a master key from an existing 24-word mnemonic.
func MasterKeyFromMnemonic(mnemonic string) (MasterKey, error) {
	var master MasterKey
	if !bip39.IsMnemonicValid(mnemonic) {
		return master, fmt.Errorf("crypto.MasterKeyFromMnemonic: invalid mnemonic")
	}
	seed := bip39.NewSeed(mnemonic, "doomsday")
	copy(master[:], seed[:32])
	zeroSlice(seed) // SECURITY: wipe seed from heap
	return master, nil
}

// doomsdayPassphraseSalt is a fixed salt for deriving master keys from passphrases.
// This is safe because the passphrase itself provides the entropy — the salt just
// needs to be unique to this application to avoid rainbow table attacks.
var doomsdayPassphraseSalt = []byte("doomsday-passphrase-kdf-v1-salt\x00")

// DeriveKeyFromPassphrase derives a MasterKey from an arbitrary passphrase
// using scrypt with a fixed application-specific salt.
// The same passphrase always produces the same key.
func DeriveKeyFromPassphrase(passphrase string) (MasterKey, error) {
	var master MasterKey
	if passphrase == "" {
		return master, fmt.Errorf("crypto.DeriveKeyFromPassphrase: passphrase is empty")
	}
	derived, err := DeriveFromPassword([]byte(passphrase), doomsdayPassphraseSalt, DefaultScryptParams())
	if err != nil {
		return master, fmt.Errorf("crypto.DeriveKeyFromPassphrase: %w", err)
	}
	copy(master[:], derived[:])
	return master, nil
}

// GenerateMasterKey generates a random 256-bit master key.
func GenerateMasterKey() (MasterKey, error) {
	var key MasterKey
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		return key, fmt.Errorf("crypto.GenerateMasterKey: %w", err)
	}
	return key, nil
}
