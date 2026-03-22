package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/spf13/cobra"
)

var errCosignNotFound = errors.New("cosign not found in PATH")

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update doomsday to the latest version",
	Long: `Checks for a newer version on GitHub and replaces the current binary.

Verifies SHA-256 checksums from the release and, if the cosign CLI is
installed, cryptographically verifies the checksums file was signed by
the official GitHub Actions release workflow.

Install cosign for full signature verification:
  https://docs.sigstore.dev/cosign/system_config/installation/`,
	RunE: runUpdate,
}


func runUpdate(cmd *cobra.Command, args []string) error {
	if version == "dev" {
		return fmt.Errorf("cannot self-update a development build; install from https://github.com/jclement/doomsday/releases")
	}

	ctx := context.Background()

	// Configure updater with SHA-256 checksum verification.
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
	if !latest.GreaterThan(v) {
		logger.Info("Already up to date", "version", v)
		return nil
	}

	logger.Info("New version available", "current", v, "latest", latest.Version())

	// Verify cosign signature on checksums before applying the update.
	tag := "v" + latest.Version()
	if err := verifyCosign(ctx, tag); err != nil {
		if errors.Is(err, errCosignNotFound) {
			logger.Warn("cosign not installed; signature verification skipped",
				"hint", "install cosign for cryptographic release verification: https://docs.sigstore.dev/cosign/system_config/installation/")
		} else {
			return fmt.Errorf("update: cosign signature verification failed: %w (this may indicate a tampered release)", err)
		}
	} else {
		logger.Info("Release signature verified via cosign")
	}

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

// verifyCosign downloads the cosign signature from the GitHub release and
// verifies checksums.txt using the cosign CLI. This ensures the checksums
// file was signed by the official GitHub Actions release workflow (keyless
// signing via Sigstore/Fulcio).
//
// Returns errCosignNotFound if cosign is not installed.
// Returns an error if cosign is installed but verification fails.
func verifyCosign(ctx context.Context, tag string) error {
	cosignPath, err := exec.LookPath("cosign")
	if err != nil {
		return errCosignNotFound
	}

	tmpDir, err := os.MkdirTemp("", "doomsday-update-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	baseURL := fmt.Sprintf("https://github.com/jclement/doomsday/releases/download/%s", tag)

	// Download checksums and signature (required).
	for _, f := range []string{"checksums.txt", "checksums.txt.sig"} {
		if err := downloadReleaseFile(ctx, baseURL+"/"+f, filepath.Join(tmpDir, f)); err != nil {
			return fmt.Errorf("download %s: %w", f, err)
		}
	}

	// Certificate file is optional; enables offline verification without Rekor lookup.
	pemPath := filepath.Join(tmpDir, "checksums.txt.pem")
	havePem := false
	if err := downloadReleaseFile(ctx, baseURL+"/checksums.txt.pem", pemPath); err == nil {
		havePem = true
	}

	// Build cosign verify-blob command.
	verifyArgs := []string{
		"verify-blob",
		filepath.Join(tmpDir, "checksums.txt"),
		"--signature", filepath.Join(tmpDir, "checksums.txt.sig"),
		"--certificate-oidc-issuer", "https://token.actions.githubusercontent.com",
		"--certificate-identity-regexp", `github\.com/jclement/doomsday`,
	}
	if havePem {
		verifyArgs = append(verifyArgs, "--certificate", pemPath)
	}

	cmd := exec.CommandContext(ctx, cosignPath, verifyArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cosign verify-blob: %s: %w", strings.TrimSpace(string(output)), err)
	}

	return nil
}

// downloadReleaseFile downloads a file from a URL to a local path.
func downloadReleaseFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	// Limit download to 10 MiB to prevent abuse.
	_, err = io.Copy(f, io.LimitReader(resp.Body, 10<<20))
	return err
}
