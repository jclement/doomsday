package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

// KeyFile is the on-disk format for storing a master key.
//
// Version 1: password-wrapped (scrypt + AES-256-GCM). All fields used.
// Version 2: plaintext (no password). Only Version and WrappedMasterKey (base64 of raw key) are set.
type KeyFile struct {
	Version          int    `json:"version"`
	KDF              string `json:"kdf,omitempty"`
	N                int    `json:"N,omitempty"`
	R                int    `json:"r,omitempty"`
	P                int    `json:"p,omitempty"`
	Salt             string `json:"salt,omitempty"`     // base64
	Nonce            string `json:"nonce,omitempty"`    // base64
	WrappedMasterKey string `json:"wrapped_master_key"` // base64
}

// IsPlaintext returns true if this is a version 2 (unencrypted) key file.
func (kf *KeyFile) IsPlaintext() bool {
	return kf.Version == 2
}

// CreatePlaintextKeyFile creates a version 2 key file that stores the master key
// without password protection. The key is base64-encoded but not encrypted.
// Use this when the key is protected by other means (filesystem permissions,
// secret manager, etc.).
func CreatePlaintextKeyFile(master MasterKey) *KeyFile {
	return &KeyFile{
		Version:          2,
		WrappedMasterKey: base64.StdEncoding.EncodeToString(master[:]),
	}
}

// CreateKeyFile creates a key file that wraps a master key with a password.
func CreateKeyFile(master MasterKey, password []byte, params ScryptParams) (*KeyFile, error) {
	salt, err := GenerateSalt()
	if err != nil {
		return nil, fmt.Errorf("crypto.CreateKeyFile: %w", err)
	}

	kek, err := DeriveFromPassword(password, salt, params)
	if err != nil {
		return nil, fmt.Errorf("crypto.CreateKeyFile: %w", err)
	}
	defer zeroKey(&kek) // SECURITY: wipe KEK after use

	block, err := aes.NewCipher(kek[:])
	if err != nil {
		return nil, fmt.Errorf("crypto.CreateKeyFile: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto.CreateKeyFile: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto.CreateKeyFile: %w", err)
	}

	wrapped := gcm.Seal(nil, nonce, master[:], AADKeyFile)

	return &KeyFile{
		Version:          1,
		KDF:              "scrypt",
		N:                params.N,
		R:                params.R,
		P:                params.P,
		Salt:             base64.StdEncoding.EncodeToString(salt),
		Nonce:            base64.StdEncoding.EncodeToString(nonce),
		WrappedMasterKey: base64.StdEncoding.EncodeToString(wrapped),
	}, nil
}

// OpenKeyFile decrypts the master key from a key file using the given password.
// For version 2 (plaintext) key files, the password is ignored.
func OpenKeyFile(kf *KeyFile, password []byte) (MasterKey, error) {
	var master MasterKey

	// Version 2: plaintext key file, no password needed.
	if kf.Version == 2 {
		raw, err := base64.StdEncoding.DecodeString(kf.WrappedMasterKey)
		if err != nil {
			return master, fmt.Errorf("crypto.OpenKeyFile: decode plaintext key: %w", err)
		}
		if len(raw) != 32 {
			return master, fmt.Errorf("crypto.OpenKeyFile: unexpected master key length %d", len(raw))
		}
		copy(master[:], raw)
		return master, nil
	}

	salt, err := base64.StdEncoding.DecodeString(kf.Salt)
	if err != nil {
		return master, fmt.Errorf("crypto.OpenKeyFile: decode salt: %w", err)
	}

	nonce, err := base64.StdEncoding.DecodeString(kf.Nonce)
	if err != nil {
		return master, fmt.Errorf("crypto.OpenKeyFile: decode nonce: %w", err)
	}

	wrapped, err := base64.StdEncoding.DecodeString(kf.WrappedMasterKey)
	if err != nil {
		return master, fmt.Errorf("crypto.OpenKeyFile: decode wrapped key: %w", err)
	}

	// SECURITY: Validate scrypt parameters from the key file to prevent
	// an attacker with filesystem access from weakening the KDF by modifying
	// the key file to use trivially low parameters (e.g., N=1).
	if kf.N < 16384 { // minimum 2^14
		return master, fmt.Errorf("crypto.OpenKeyFile: scrypt N=%d is below minimum (16384)", kf.N)
	}
	if kf.R < 8 {
		return master, fmt.Errorf("crypto.OpenKeyFile: scrypt r=%d is below minimum (8)", kf.R)
	}
	if kf.P < 1 {
		return master, fmt.Errorf("crypto.OpenKeyFile: scrypt p=%d is below minimum (1)", kf.P)
	}

	params := ScryptParams{N: kf.N, R: kf.R, P: kf.P}
	kek, err := DeriveFromPassword(password, salt, params)
	if err != nil {
		return master, fmt.Errorf("crypto.OpenKeyFile: %w", err)
	}
	defer zeroKey(&kek) // SECURITY: wipe KEK after use

	block, err := aes.NewCipher(kek[:])
	if err != nil {
		return master, fmt.Errorf("crypto.OpenKeyFile: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return master, fmt.Errorf("crypto.OpenKeyFile: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, wrapped, AADKeyFile)
	if err != nil {
		return master, fmt.Errorf("crypto.OpenKeyFile: wrong password or corrupted key file")
	}
	defer zeroSlice(plaintext) // SECURITY: wipe plaintext master key from heap

	if len(plaintext) != 32 {
		return master, fmt.Errorf("crypto.OpenKeyFile: unexpected master key length %d", len(plaintext))
	}

	copy(master[:], plaintext)
	return master, nil
}

// Marshal serializes a KeyFile to JSON.
func (kf *KeyFile) Marshal() ([]byte, error) {
	data, err := json.MarshalIndent(kf, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("crypto.KeyFile.Marshal: %w", err)
	}
	return data, nil
}

// UnmarshalKeyFile deserializes a KeyFile from JSON.
func UnmarshalKeyFile(data []byte) (*KeyFile, error) {
	var kf KeyFile
	if err := json.Unmarshal(data, &kf); err != nil {
		return nil, fmt.Errorf("crypto.UnmarshalKeyFile: %w", err)
	}
	return &kf, nil
}
