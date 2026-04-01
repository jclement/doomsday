package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/spf13/cobra"
)

var updateFlagForce bool

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update doomsday to the latest version",
	Long: `Checks for a newer version on GitHub and replaces the current binary.

Verifies the release's Sigstore signature to ensure it was built by the
official GitHub Actions release workflow. No external tools required —
verification uses the Sigstore trusted root.

Examples:
  doomsday update
  doomsday update --force`,
	RunE: runUpdate,
}

func init() {
	updateCmd.Flags().BoolVar(&updateFlagForce, "force", false, "force update even if already up to date")
}

func runUpdate(cmd *cobra.Command, args []string) error {
	if version == "dev" {
		return fmt.Errorf("cannot self-update a development build; install from https://github.com/jclement/doomsday/releases")
	}

	ctx := context.Background()

	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
	})
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

	slug := selfupdate.NewRepositorySlug("jclement", "doomsday")
	latest, found, err := updater.DetectLatest(ctx, slug)
	if err != nil {
		return fmt.Errorf("update: check for updates: %w", err)
	}
	if !found {
		return fmt.Errorf("update: no compatible release found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	v := strings.TrimPrefix(version, "v")
	if !latest.GreaterThan(v) && !updateFlagForce {
		logger.Info("Already up to date", "version", v)
		return nil
	}

	logger.Info("New version available", "current", v, "latest", latest.Version())

	tag := "v" + latest.Version()
	if err := verifyRelease(ctx, tag); err != nil {
		return fmt.Errorf("update: signature verification failed: %w", err)
	}
	logger.Info("Release signature verified (Sigstore)")

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("update: locate current binary: %w", err)
	}

	if err := updater.UpdateTo(ctx, latest, exe); err != nil {
		return fmt.Errorf("update: apply: %w", err)
	}

	logger.Info("Updated successfully", "version", latest.Version())
	return nil
}

// verifyRelease downloads the checksums and Sigstore bundle from the GitHub
// release and verifies the signature. Supports both the new protobuf-based
// bundle format and the legacy cosign bundle format.
func verifyRelease(ctx context.Context, tag string) error {
	baseURL := fmt.Sprintf("https://github.com/jclement/doomsday/releases/download/%s", tag)

	checksumData, err := downloadBytes(ctx, baseURL+"/checksums.txt")
	if err != nil {
		return fmt.Errorf("download checksums.txt: %w", err)
	}

	bundleData, err := downloadBytes(ctx, baseURL+"/checksums.txt.sigstore.json")
	if err != nil {
		return fmt.Errorf("download sigstore bundle: %w", err)
	}

	// Try the new sigstore-go protobuf bundle format first.
	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleData); err == nil {
		return verifyNewBundle(checksumData, &b)
	}

	// Fall back to legacy cosign bundle format.
	return verifyLegacyBundle(checksumData, bundleData)
}

// verifyNewBundle verifies using the sigstore-go library with TUF trusted root.
func verifyNewBundle(checksumData []byte, b *bundle.Bundle) error {
	trustedRoot, err := root.NewLiveTrustedRoot(tuf.DefaultOptions())
	if err != nil {
		return fmt.Errorf("loading trusted root: %w", err)
	}

	verifier, err := verify.NewVerifier(trustedRoot,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return fmt.Errorf("creating verifier: %w", err)
	}

	certID, err := verify.NewShortCertificateIdentity(
		"https://token.actions.githubusercontent.com", "",
		"", "^https://github.com/jclement/doomsday/",
	)
	if err != nil {
		return fmt.Errorf("creating certificate identity: %w", err)
	}

	_, err = verifier.Verify(b,
		verify.NewPolicy(
			verify.WithArtifact(bytes.NewReader(checksumData)),
			verify.WithCertificateIdentity(certID),
		),
	)
	if err != nil {
		return fmt.Errorf("sigstore verification failed: %w", err)
	}
	return nil
}

// legacyCosignBundle is the old cosign sign-blob --bundle format.
type legacyCosignBundle struct {
	Base64Signature string      `json:"base64Signature"`
	Cert            string      `json:"cert"`
	RekorBundle     legacyRekor `json:"rekorBundle"`
}

type legacyRekor struct {
	Payload legacyRekorPayload `json:"Payload"`
}

type legacyRekorPayload struct {
	IntegratedTime int64 `json:"integratedTime"`
}

// verifyLegacyBundle verifies the old cosign bundle format (base64Signature + cert).
func verifyLegacyBundle(checksumData, bundleJSON []byte) error {
	var ob legacyCosignBundle
	if err := json.Unmarshal(bundleJSON, &ob); err != nil {
		return fmt.Errorf("parsing bundle: %w", err)
	}
	if ob.Base64Signature == "" || ob.Cert == "" {
		return fmt.Errorf("bundle missing signature or certificate")
	}

	sig, err := base64.StdEncoding.DecodeString(ob.Base64Signature)
	if err != nil {
		return fmt.Errorf("decoding signature: %w", err)
	}

	certPEM, err := base64.StdEncoding.DecodeString(ob.Cert)
	if err != nil {
		return fmt.Errorf("decoding certificate: %w", err)
	}

	cert, err := parsePEMCert(certPEM)
	if err != nil {
		return err
	}

	// Verify certificate chain via TUF trusted root.
	trustedRoot, err := root.NewLiveTrustedRoot(tuf.DefaultOptions())
	if err != nil {
		return fmt.Errorf("loading trusted root: %w", err)
	}

	certVerified := false
	verifyTime := time.Unix(ob.RekorBundle.Payload.IntegratedTime, 0)
	for _, ca := range trustedRoot.FulcioCertificateAuthorities() {
		if _, err := ca.Verify(cert, verifyTime); err == nil {
			certVerified = true
			break
		}
	}
	if !certVerified {
		return fmt.Errorf("certificate not issued by a trusted Fulcio CA")
	}

	// Check identity.
	foundIdentity := false
	for _, uri := range cert.URIs {
		if strings.HasPrefix(uri.String(), "https://github.com/jclement/doomsday/") {
			foundIdentity = true
			break
		}
	}
	if !foundIdentity {
		return fmt.Errorf("certificate identity does not match expected GitHub workflow")
	}

	// Verify ECDSA signature.
	ecPub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("certificate public key is not ECDSA")
	}
	digest := sha256.Sum256(checksumData)
	if !ecdsa.VerifyASN1(ecPub, digest[:], sig) {
		return fmt.Errorf("ECDSA signature verification failed")
	}

	return nil
}

func parsePEMCert(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func downloadBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 10<<20))
}
