package crypto

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"strings"
	"testing"

	"github.com/jclement/doomsday/internal/types"
)

func testMasterKey(t *testing.T) MasterKey {
	t.Helper()
	key, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey: %v", err)
	}
	return key
}

func TestDeriveSubKeys(t *testing.T) {
	master := testMasterKey(t)
	sk, err := DeriveSubKeys(master)
	if err != nil {
		t.Fatalf("DeriveSubKeys: %v", err)
	}

	// All sub-keys should be different from each other
	keys := [][32]byte{sk.Data, sk.Tree, sk.Index, sk.Snapshot, sk.Config, sk.ContentID}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] == keys[j] {
				t.Errorf("sub-keys %d and %d are identical", i, j)
			}
		}
	}

	// Sub-keys should be different from master
	for i, k := range keys {
		if k == [32]byte(master) {
			t.Errorf("sub-key %d equals master key", i)
		}
	}
}

func TestDeriveSubKeys_Deterministic(t *testing.T) {
	master := testMasterKey(t)
	sk1, err := DeriveSubKeys(master)
	if err != nil {
		t.Fatal(err)
	}
	sk2, err := DeriveSubKeys(master)
	if err != nil {
		t.Fatal(err)
	}
	if *sk1 != *sk2 {
		t.Error("same master key should produce same sub-keys")
	}
}

func TestEncryptDecryptBlob(t *testing.T) {
	master := testMasterKey(t)
	sk, _ := DeriveSubKeys(master)

	plaintext := []byte("the end of the world as we know it")
	blobID := ContentID(sk.ContentID, plaintext)

	ciphertext, err := EncryptBlob(sk.Data, blobID, types.BlobTypeData, "repo-123", plaintext)
	if err != nil {
		t.Fatalf("EncryptBlob: %v", err)
	}

	// Ciphertext should differ from plaintext
	if bytes.Equal(ciphertext, plaintext) {
		t.Error("ciphertext equals plaintext")
	}

	// Decrypt
	decrypted, err := DecryptBlob(sk.Data, blobID, types.BlobTypeData, "repo-123", ciphertext)
	if err != nil {
		t.Fatalf("DecryptBlob: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Error("decrypted data doesn't match plaintext")
	}
}

func TestEncryptBlob_WrongKey(t *testing.T) {
	master1 := testMasterKey(t)
	master2 := testMasterKey(t)
	sk1, _ := DeriveSubKeys(master1)
	sk2, _ := DeriveSubKeys(master2)

	plaintext := []byte("secret data")
	blobID := ContentID(sk1.ContentID, plaintext)

	ciphertext, _ := EncryptBlob(sk1.Data, blobID, types.BlobTypeData, "repo", plaintext)

	_, err := DecryptBlob(sk2.Data, blobID, types.BlobTypeData, "repo", ciphertext)
	if err == nil {
		t.Error("expected decryption to fail with wrong key")
	}
}

func TestEncryptBlob_WrongBlobType(t *testing.T) {
	master := testMasterKey(t)
	sk, _ := DeriveSubKeys(master)

	plaintext := []byte("data blob pretending to be tree")
	blobID := ContentID(sk.ContentID, plaintext)

	ciphertext, _ := EncryptBlob(sk.Data, blobID, types.BlobTypeData, "repo", plaintext)

	// Try to decrypt as tree blob — should fail due to AAD mismatch
	_, err := DecryptBlob(sk.Data, blobID, types.BlobTypeTree, "repo", ciphertext)
	if err == nil {
		t.Error("expected decryption to fail with wrong blob type (AAD mismatch)")
	}
}

func TestEncryptBlob_WrongRepoID(t *testing.T) {
	master := testMasterKey(t)
	sk, _ := DeriveSubKeys(master)

	plaintext := []byte("cross-repo attack")
	blobID := ContentID(sk.ContentID, plaintext)

	ciphertext, _ := EncryptBlob(sk.Data, blobID, types.BlobTypeData, "repo-a", plaintext)

	_, err := DecryptBlob(sk.Data, blobID, types.BlobTypeData, "repo-b", ciphertext)
	if err == nil {
		t.Error("expected decryption to fail with wrong repo ID (AAD mismatch)")
	}
}

func TestEncryptBlob_BitFlipDetection(t *testing.T) {
	master := testMasterKey(t)
	sk, _ := DeriveSubKeys(master)

	plaintext := make([]byte, 1024)
	rand.Read(plaintext)
	blobID := ContentID(sk.ContentID, plaintext)

	ciphertext, _ := EncryptBlob(sk.Data, blobID, types.BlobTypeData, "repo", plaintext)

	// Flip a bit in the ciphertext body (past the nonce)
	corrupted := make([]byte, len(ciphertext))
	copy(corrupted, ciphertext)
	corrupted[20] ^= 0x01

	_, err := DecryptBlob(sk.Data, blobID, types.BlobTypeData, "repo", corrupted)
	if err == nil {
		t.Error("expected decryption to fail after bit flip")
	}
}

func TestEncryptDecryptRaw(t *testing.T) {
	var key [32]byte
	rand.Read(key[:])
	plaintext := []byte("config or index data")

	ciphertext, err := EncryptRaw(key, plaintext, AADConfig)
	if err != nil {
		t.Fatal(err)
	}

	decrypted, err := DecryptRaw(key, ciphertext, AADConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Error("roundtrip failed")
	}
}

func TestContentID_Keyed(t *testing.T) {
	var key1, key2 [32]byte
	rand.Read(key1[:])
	rand.Read(key2[:])
	data := []byte("same data, different keys")

	id1 := ContentID(key1, data)
	id2 := ContentID(key2, data)

	if id1 == id2 {
		t.Error("different keys should produce different content IDs")
	}
}

func TestContentID_Deterministic(t *testing.T) {
	var key [32]byte
	rand.Read(key[:])
	data := []byte("deterministic test")

	id1 := ContentID(key, data)
	id2 := ContentID(key, data)
	if id1 != id2 {
		t.Error("same key+data should produce same content ID")
	}
}

func TestKeyFile_Roundtrip(t *testing.T) {
	master := testMasterKey(t)
	password := []byte("test-password-123")

	// Use fast params for tests
	params := ScryptParams{N: 1 << 14, R: 8, P: 1}

	kf, err := CreateKeyFile(master, password, params)
	if err != nil {
		t.Fatal(err)
	}

	// Marshal / unmarshal
	data, err := kf.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	kf2, err := UnmarshalKeyFile(data)
	if err != nil {
		t.Fatal(err)
	}

	// Open with correct password
	recovered, err := OpenKeyFile(kf2, password)
	if err != nil {
		t.Fatal(err)
	}

	if recovered != master {
		t.Error("recovered master key doesn't match original")
	}
}

func TestKeyFile_WrongPassword(t *testing.T) {
	master := testMasterKey(t)
	params := ScryptParams{N: 1 << 14, R: 8, P: 1}
	kf, _ := CreateKeyFile(master, []byte("correct"), params)
	_, err := OpenKeyFile(kf, []byte("wrong"))
	if err == nil {
		t.Error("expected error with wrong password")
	}
}

func TestGenerateRecoveryPhrase(t *testing.T) {
	mnemonic, master, err := GenerateRecoveryPhrase()
	if err != nil {
		t.Fatal(err)
	}

	// Should be 24 words
	words := strings.Fields(mnemonic)
	if len(words) != 24 {
		t.Errorf("expected 24 words, got %d", len(words))
	}

	// Master key should not be zero
	if master == (MasterKey{}) {
		t.Error("master key is zero")
	}

	// Re-derive from mnemonic should match
	recovered, err := MasterKeyFromMnemonic(mnemonic)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != master {
		t.Error("re-derived master key doesn't match")
	}
}

func TestMasterKeyFromMnemonic_Invalid(t *testing.T) {
	_, err := MasterKeyFromMnemonic("not a valid mnemonic phrase")
	if err == nil {
		t.Error("expected error for invalid mnemonic")
	}
}

// TestDeriveSubKeys_GoldenVector verifies HKDF sub-key derivation against
// hardcoded test vectors. A fixed master key (all 0x01 bytes) must always
// produce exactly these sub-keys. Any change means the key derivation is
// broken and existing repositories can no longer be opened.
func TestDeriveSubKeys_GoldenVector(t *testing.T) {
	var master MasterKey
	for i := range master {
		master[i] = 0x01
	}

	sk, err := DeriveSubKeys(master)
	if err != nil {
		t.Fatalf("DeriveSubKeys: %v", err)
	}

	want := map[string]string{
		"Data":      "b46ea51a630cb3b259acdbf41a5937266c25be6376bc425817ec1af222cc7e9a",
		"Tree":      "b3f82d39b5a6fb84d38281cbd9f8481b9f8743546b34e6fc2a52cee6b15a68f5",
		"Index":     "2ce96e8b25a4d34a1c3a83283f78b9379539a1fd8bfecb4b43e836a4b6321ecd",
		"Snapshot":  "0d523913c15b96d74d6a3813df60396afe3814e017ef22dddada7f54d870ecfa",
		"Config":    "c906ba9775bd49586efcd2d1ebc58e08b40f545a5f164320e986ecaab13629e8",
		"ContentID": "f6030cb5b7c243401e627863b6adb591288e32f8df58f63dc6438fc53ce6a1e8",
	}

	got := map[string]string{
		"Data":      fmt.Sprintf("%x", sk.Data),
		"Tree":      fmt.Sprintf("%x", sk.Tree),
		"Index":     fmt.Sprintf("%x", sk.Index),
		"Snapshot":  fmt.Sprintf("%x", sk.Snapshot),
		"Config":    fmt.Sprintf("%x", sk.Config),
		"ContentID": fmt.Sprintf("%x", sk.ContentID),
	}

	for name, wantHex := range want {
		gotHex := got[name]
		if gotHex != wantHex {
			t.Errorf("SubKey %s:\n  got  %s\n  want %s", name, gotHex, wantHex)
		}
	}
}

// TestContentID_GoldenVector verifies HMAC-SHA256 content ID computation
// against a hardcoded test vector. The ContentID sub-key derived from an
// all-0x01 master key, applied to "hello world", must always produce
// exactly this blob ID.
func TestContentID_GoldenVector(t *testing.T) {
	var master MasterKey
	for i := range master {
		master[i] = 0x01
	}

	sk, err := DeriveSubKeys(master)
	if err != nil {
		t.Fatalf("DeriveSubKeys: %v", err)
	}

	blobID := ContentID(sk.ContentID, []byte("hello world"))
	want := "886031fe8be5d32efcff7e877e5d4c17630d3b21b2b15a41dcc48b997535aea6"
	got := blobID.String()
	if got != want {
		t.Errorf("ContentID(\"hello world\"):\n  got  %s\n  want %s", got, want)
	}
}

// TestEncryptRaw_WrongAAD verifies that decrypting with a different AAD label
// than was used for encryption fails. This ensures domain separation: data
// encrypted as config cannot be decrypted as index.
func TestEncryptRaw_WrongAAD(t *testing.T) {
	var key [32]byte
	for i := range key {
		key[i] = 0x42
	}
	plaintext := []byte("domain-separated payload")

	ciphertext, err := EncryptRaw(key, plaintext, AADConfig)
	if err != nil {
		t.Fatalf("EncryptRaw: %v", err)
	}

	_, err = DecryptRaw(key, ciphertext, AADIndex)
	if err == nil {
		t.Error("expected decryption to fail with wrong AAD (AADConfig vs AADIndex)")
	}
}

// TestEncryptDecryptBlob_EmptyPlaintext verifies that empty plaintext
// roundtrips correctly through encrypt/decrypt. Edge case: some AEAD
// implementations handle zero-length plaintext differently.
func TestEncryptDecryptBlob_EmptyPlaintext(t *testing.T) {
	var master MasterKey
	for i := range master {
		master[i] = 0xAA
	}

	sk, err := DeriveSubKeys(master)
	if err != nil {
		t.Fatalf("DeriveSubKeys: %v", err)
	}

	plaintext := []byte{}
	blobID := ContentID(sk.ContentID, plaintext)

	ciphertext, err := EncryptBlob(sk.Data, blobID, types.BlobTypeData, "repo-empty", plaintext)
	if err != nil {
		t.Fatalf("EncryptBlob: %v", err)
	}

	// Ciphertext must be non-empty (nonce + GCM tag at minimum)
	if len(ciphertext) == 0 {
		t.Fatal("ciphertext is empty for empty plaintext; expected nonce+tag overhead")
	}

	decrypted, err := DecryptBlob(sk.Data, blobID, types.BlobTypeData, "repo-empty", ciphertext)
	if err != nil {
		t.Fatalf("DecryptBlob: %v", err)
	}

	if len(decrypted) != 0 {
		t.Errorf("decrypted length = %d, want 0", len(decrypted))
	}
}

func TestGenerateRepoID(t *testing.T) {
	id1, err := GenerateRepoID()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := GenerateRepoID()
	if err != nil {
		t.Fatal(err)
	}
	if id1 == id2 {
		t.Error("two generated repo IDs should differ")
	}
	if len(id1) != 32 { // 16 bytes hex-encoded = 32 chars
		t.Errorf("repo ID length = %d, want 32", len(id1))
	}
}
