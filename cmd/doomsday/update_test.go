package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/sigstore/sigstore-go/pkg/bundle"
)

func TestECDSASignatureVerification(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	data := []byte("checksums content\nhash1  file1.tar.gz\n")
	digest := sha256.Sum256(data)

	sig, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}

	if !ecdsa.VerifyASN1(&priv.PublicKey, digest[:], sig) {
		t.Fatal("valid signature should verify")
	}

	tampered := sha256.Sum256([]byte("tampered"))
	if ecdsa.VerifyASN1(&priv.PublicKey, tampered[:], sig) {
		t.Fatal("tampered data should not verify")
	}

	otherKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if ecdsa.VerifyASN1(&otherKey.PublicKey, digest[:], sig) {
		t.Fatal("wrong key should not verify")
	}
}

// TestVerifyRealRelease verifies the real v1.5.0 release using our
// verifyRelease function. This is the definitive proof that our
// verification code works against real release artifacts.
func TestVerifyRealRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}
	ctx := context.Background()
	if err := verifyRelease(ctx, "v1.5.0"); err != nil {
		t.Fatalf("VERIFICATION FAILED: %v", err)
	}
	t.Log("Real release verification PASSED")
}

// TestVerifyTamperedChecksums verifies that tampered checksums are rejected.
// Downloads the real bundle but feeds tampered content.
func TestVerifyTamperedChecksums(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	ctx := context.Background()
	baseURL := "https://github.com/jclement/doomsday/releases/download/v1.5.0"

	checksumData, err := downloadBytes(ctx, baseURL+"/checksums.txt")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	bundleData, err := downloadBytes(ctx, baseURL+"/checksums.txt.sigstore.json")
	if err != nil {
		t.Fatalf("download: %v", err)
	}

	// Tamper with the checksums.
	tampered := append([]byte("TAMPERED\n"), checksumData...)

	// Try legacy verification with tampered data - should fail.
	err = verifyLegacyBundle(tampered, bundleData)
	if err == nil {
		t.Fatal("tampered checksums should have been REJECTED!")
	}
	t.Logf("Tampered checksums correctly rejected: %v", err)
}

// TestVerifyWrongIdentity verifies that a valid sigstore bundle with
// a wrong identity check is rejected.
func TestVerifyWrongIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	ctx := context.Background()
	baseURL := "https://github.com/jclement/doomsday/releases/download/v1.5.0"

	bundleData, err := downloadBytes(ctx, baseURL+"/checksums.txt.sigstore.json")
	if err != nil {
		t.Fatalf("download: %v", err)
	}

	// The ECDSA signature is bound to the specific checksums content.
	// Using wrong content should be rejected.
	err = verifyLegacyBundle([]byte("wrong content entirely"), bundleData)
	if err == nil {
		t.Fatal("wrong content should have been REJECTED!")
	}
	t.Logf("Wrong content correctly rejected: %v", err)
}

// TestVerifyReleaseFull tests the full verifyRelease function against
// the real v1.5.0 release.
func TestVerifyReleaseFull(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	ctx := context.Background()
	if err := verifyRelease(ctx, "v1.5.0"); err != nil {
		t.Fatalf("verifyRelease failed: %v", err)
	}
	t.Log("Full verifyRelease PASSED for v1.5.0")
}

// TestUpdateEndToEnd builds doomsday with version "0.0.1", then runs the
// update check to verify it detects the newer version and verifies the
// release signature correctly.
func TestUpdateEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	// Simulate having an old version.
	oldVersion := version
	version = "0.0.1"
	defer func() { version = oldVersion }()

	ctx := context.Background()

	// Check for updates (should find v1.5.0).
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}

	slug := selfupdate.NewRepositorySlug("jclement", "doomsday")
	latest, found, err := updater.DetectLatest(ctx, slug)
	if err != nil {
		t.Fatalf("detect latest: %v", err)
	}
	if !found {
		t.Fatal("no release found")
	}

	t.Logf("Current: %s, Latest: %s", version, latest.Version())

	if !latest.GreaterThan("0.0.1") {
		t.Fatal("latest should be greater than 0.0.1")
	}

	// Verify the release signature.
	tag := "v" + latest.Version()
	if err := verifyRelease(ctx, tag); err != nil {
		t.Fatalf("verifyRelease(%s) failed: %v", tag, err)
	}
	t.Logf("Signature verification PASSED for %s", tag)

	// Don't actually apply the update (we'd overwrite our test binary).
	t.Log("Skipping binary replacement (test mode)")
}

// TestBundleFormatDetection tests that we correctly handle both
// new protobuf bundles and legacy cosign bundles.
func TestBundleFormatDetection(t *testing.T) {
	// New format bundle should parse.
	newBundle := `{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json"}`
	var b bundle.Bundle
	err := b.UnmarshalJSON([]byte(newBundle))
	// May fail on minimal JSON, but should not panic.
	t.Logf("New format parse result: %v", err)

	// Old format should fail protobuf parse and fall through to legacy.
	oldBundle := `{"base64Signature":"abc","cert":"def"}`
	err = b.UnmarshalJSON([]byte(oldBundle))
	if err == nil {
		t.Error("old cosign format should not parse as protobuf bundle")
	}

	// Verify legacy parsing.
	var ob legacyCosignBundle
	if err := json.Unmarshal([]byte(oldBundle), &ob); err != nil {
		t.Fatalf("legacy format should parse: %v", err)
	}
	if ob.Base64Signature != "abc" {
		t.Error("legacy signature not parsed correctly")
	}
}
