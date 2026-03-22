package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/jclement/doomsday/internal/types"
	"golang.org/x/crypto/hkdf"
)

// EncryptBlob encrypts data using a per-blob derived key with AES-256-GCM.
//
// Key derivation: blobKey = HKDF-SHA256(subKey, blobID) — each blob gets a unique key.
// AAD binds: blob ID + blob type + repo ID (prevents cross-repo/cross-type substitution).
// Nonce: 96-bit random from crypto/rand (safe because each key is used exactly once).
//
// Output format: [12-byte nonce][ciphertext+tag]
func EncryptBlob(subKey [32]byte, blobID types.BlobID, blobType types.BlobType, repoID string, plaintext []byte) ([]byte, error) {
	// Derive per-blob key
	blobKey, err := deriveBlobKey(subKey, blobID)
	if err != nil {
		return nil, fmt.Errorf("crypto.EncryptBlob: %w", err)
	}
	defer zeroKey(&blobKey)

	block, err := aes.NewCipher(blobKey[:])
	if err != nil {
		return nil, fmt.Errorf("crypto.EncryptBlob: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto.EncryptBlob: %w", err)
	}

	// Random nonce (safe: key is used exactly once)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto.EncryptBlob: generate nonce: %w", err)
	}

	// Build AAD: blobID || blobType || repoID
	aad := buildAAD(blobID, blobType, repoID)

	// Encrypt: output = nonce || ciphertext+tag
	ciphertext := gcm.Seal(nonce, nonce, plaintext, aad)
	return ciphertext, nil
}

// DecryptBlob decrypts data encrypted by EncryptBlob.
// Verifies AAD binding (blob ID, type, repo ID). Returns error on any tampering.
func DecryptBlob(subKey [32]byte, blobID types.BlobID, blobType types.BlobType, repoID string, ciphertext []byte) ([]byte, error) {
	blobKey, err := deriveBlobKey(subKey, blobID)
	if err != nil {
		return nil, fmt.Errorf("crypto.DecryptBlob: %w", err)
	}
	defer zeroKey(&blobKey)

	block, err := aes.NewCipher(blobKey[:])
	if err != nil {
		return nil, fmt.Errorf("crypto.DecryptBlob: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto.DecryptBlob: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("crypto.DecryptBlob: %w: ciphertext too short", types.ErrDecryptFailed)
	}

	nonce := ciphertext[:nonceSize]
	encrypted := ciphertext[nonceSize:]
	aad := buildAAD(blobID, blobType, repoID)

	plaintext, err := gcm.Open(nil, nonce, encrypted, aad)
	if err != nil {
		return nil, fmt.Errorf("crypto.DecryptBlob: %w", types.ErrDecryptFailed)
	}

	return plaintext, nil
}

// EncryptRaw encrypts data with a key directly (no per-blob derivation).
// Used for repo config, index files, snapshots, and pack headers.
// The aad parameter provides domain separation — callers should pass a unique
// label for each usage context (e.g., "repo-config", "snapshot").
// Output format: [12-byte nonce][ciphertext+tag]
func EncryptRaw(key [32]byte, plaintext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("crypto.EncryptRaw: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto.EncryptRaw: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto.EncryptRaw: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, aad), nil
}

// DecryptRaw decrypts data encrypted by EncryptRaw.
// The aad parameter must match what was passed to EncryptRaw.
func DecryptRaw(key [32]byte, ciphertext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("crypto.DecryptRaw: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto.DecryptRaw: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("crypto.DecryptRaw: %w: ciphertext too short", types.ErrDecryptFailed)
	}
	plaintext, err := gcm.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], aad)
	if err != nil {
		return nil, fmt.Errorf("crypto.DecryptRaw: %w", types.ErrDecryptFailed)
	}
	return plaintext, nil
}

// deriveBlobKey derives a unique encryption key for a specific blob.
func deriveBlobKey(subKey [32]byte, blobID types.BlobID) ([32]byte, error) {
	var key [32]byte
	r := hkdf.New(sha256.New, subKey[:], nil, blobID[:])
	if _, err := io.ReadFull(r, key[:]); err != nil {
		return key, fmt.Errorf("derive blob key: %w", err)
	}
	return key, nil
}

// buildAAD constructs the authenticated associated data for GCM.
// Format: blobID (32 bytes) || blobType (1 byte, uint8 LE) || repoID (variable, UTF-8)
func buildAAD(blobID types.BlobID, blobType types.BlobType, repoID string) []byte {
	aad := make([]byte, 32+1+len(repoID))
	copy(aad[:32], blobID[:])
	aad[32] = byte(blobType)
	copy(aad[33:], repoID)
	return aad
}

// ContentID computes the HMAC-SHA256 content identifier for a chunk of data.
// Uses a keyed HMAC (not plain SHA-256) to prevent confirmation-of-file attacks.
func ContentID(contentIDKey [32]byte, data []byte) types.BlobID {
	return HMAC256(contentIDKey, data)
}

// HMAC256 computes HMAC-SHA256(key, message) and returns a BlobID.
// Uses Go's standard crypto/hmac package for correctness and auditability.
func HMAC256(key [32]byte, message []byte) types.BlobID {
	mac := hmac.New(sha256.New, key[:])
	mac.Write(message)
	var id types.BlobID
	copy(id[:], mac.Sum(nil))
	return id
}

// zeroKey overwrites a 32-byte key with zeros.
func zeroKey(k *[32]byte) {
	for i := range k {
		k[i] = 0
	}
}

// zeroSlice overwrites a byte slice with zeros.
func zeroSlice(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// AAD labels for domain separation in EncryptRaw/DecryptRaw.
var (
	AADConfig     = []byte("doomsday-repo-config")
	AADSnapshot   = []byte("doomsday-snapshot")
	AADIndex      = []byte("doomsday-index")
	AADPackHeader = []byte("doomsday-pack-header")
	AADKeyFile    = []byte("doomsday-keyfile-v1")
)

// RepoIDSize is the byte length of a repository ID.
const RepoIDSize = 16

// GenerateRepoID generates a random 16-byte repository identifier.
func GenerateRepoID() (string, error) {
	b := make([]byte, RepoIDSize)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("crypto.GenerateRepoID: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}
