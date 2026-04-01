package e2e_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	sftpBackend "github.com/jclement/doomsday/internal/backend/sftp"
	"github.com/jclement/doomsday/internal/backup"
	"github.com/jclement/doomsday/internal/check"
	"github.com/jclement/doomsday/internal/crypto"
	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/restore"
	"github.com/jclement/doomsday/internal/server"
	"golang.org/x/crypto/ssh"
)

// sftpTestEnv bundles everything needed for E2E SFTP tests.
type sftpTestEnv struct {
	t         *testing.T
	ctx       context.Context
	cancel    context.CancelFunc
	sourceDir string
	masterKey crypto.MasterKey
	backend   *sftpBackend.Backend
	repo      *repo.Repository

	// Connection details for reconnecting.
	host          string
	port          string
	clientKeyPath string
	knownHostsCb  ssh.HostKeyCallback
}

// newSFTPTestEnv starts a doomsday SFTP server, connects an SFTP client backend,
// and initializes a repo through the SFTP channel.
func newSFTPTestEnv(t *testing.T) *sftpTestEnv {
	t.Helper()

	// Disable SSH agent to prevent it from interfering with test key auth.
	t.Setenv("SSH_AUTH_SOCK", "")

	// Generate SSH host key (Ed25519).
	_, hostPrivKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}

	hostKeyPEM, err := ssh.MarshalPrivateKey(hostPrivKey, "")
	if err != nil {
		t.Fatalf("marshal host key: %v", err)
	}

	hostKeyPEMBytes := pem.EncodeToMemory(hostKeyPEM)

	// Generate SSH client key (Ed25519).
	_, clientPrivKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}

	clientSigner, err := ssh.NewSignerFromKey(clientPrivKey)
	if err != nil {
		t.Fatalf("client signer: %v", err)
	}

	clientPubKey := clientSigner.PublicKey()

	// Write client private key to file for the SFTP backend.
	clientKeyPEM, err := ssh.MarshalPrivateKey(clientPrivKey, "")
	if err != nil {
		t.Fatalf("marshal client key: %v", err)
	}
	clientKeyPath := filepath.Join(t.TempDir(), "client_key")
	if err := os.WriteFile(clientKeyPath, pem.EncodeToMemory(clientKeyPEM), 0600); err != nil {
		t.Fatalf("write client key: %v", err)
	}

	// Set up directories.
	dataDir := t.TempDir()
	sourceDir := t.TempDir()

	// Create a test listener on random port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Start the doomsday SFTP server in background.
	serverCfg := server.Config{
		HostKeyPEM: hostKeyPEMBytes,
		DataDir:    dataDir,
		Clients: []server.ClientConfig{
			{
				Name:       "testclient",
				PublicKey:  clientPubKey,
				QuotaBytes: 0, // unlimited
			},
		},
		Listener: ln,
	}

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.Start(ctx, serverCfg)
	}()

	// Give the server a moment to start accepting.
	time.Sleep(50 * time.Millisecond)

	// Parse the host key for known_hosts verification.
	hostPubKey, err := ssh.NewPublicKey(hostPrivKey.Public())
	if err != nil {
		cancel()
		t.Fatalf("host public key: %v", err)
	}

	// Write a known_hosts file for the client.
	addr := ln.Addr().String()
	host, port, _ := net.SplitHostPort(addr)
	knownHostLine := fmt.Sprintf("[%s]:%s %s", host, port, string(ssh.MarshalAuthorizedKey(hostPubKey)))
	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHostsPath, []byte(knownHostLine), 0600); err != nil {
		cancel()
		t.Fatalf("write known_hosts: %v", err)
	}

	// Connect SFTP client backend to the server.
	hostKeyCallback, err := sftpBackend.HostKeyFile(knownHostsPath)
	if err != nil {
		cancel()
		t.Fatalf("host key callback: %v", err)
	}

	// The basePath is relative to the client's jail: / maps to dataDir/testclient/
	backend, err := sftpBackend.New(host, port, "testclient", "/repo", clientKeyPath, "", "", hostKeyCallback)
	if err != nil {
		cancel()
		t.Fatalf("sftp.New: %v", err)
	}

	// Generate master key and init repo through SFTP.
	var masterKey crypto.MasterKey
	if _, err := rand.Read(masterKey[:]); err != nil {
		backend.Close()
		cancel()
		t.Fatalf("generate master key: %v", err)
	}

	r, err := repo.Init(ctx, backend, masterKey)
	if err != nil {
		backend.Close()
		cancel()
		t.Fatalf("repo.Init via SFTP: %v", err)
	}

	env := &sftpTestEnv{
		t:             t,
		ctx:           ctx,
		cancel:        cancel,
		sourceDir:     sourceDir,
		masterKey:     masterKey,
		backend:       backend,
		repo:          r,
		host:          host,
		port:          port,
		clientKeyPath: clientKeyPath,
		knownHostsCb:  hostKeyCallback,
	}

	t.Cleanup(func() {
		// Close the current backend (which may have been replaced by reconnect())
		// so the SFTP session ends and the server can shut down cleanly.
		// Without this, cancel() + <-serverErrCh deadlocks because
		// server.Start waits for active connections to finish.
		env.backend.Close()
		cancel()
		<-serverErrCh
	})

	return env
}

// sftpWriteFile creates a file in the source directory.
func (e *sftpTestEnv) sftpWriteFile(relPath, content string) {
	e.t.Helper()
	absPath := filepath.Join(e.sourceDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		e.t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		e.t.Fatalf("WriteFile %s: %v", relPath, err)
	}
}

// sftpWriteBinaryFile creates a file with pseudo-random binary data.
func (e *sftpTestEnv) sftpWriteBinaryFile(relPath string, size int) {
	e.t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((i*31 + 17) % 256)
	}
	absPath := filepath.Join(e.sourceDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		e.t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(absPath, data, 0644); err != nil {
		e.t.Fatalf("WriteFile %s: %v", relPath, err)
	}
}

// sftpBackup runs a backup and returns the snapshot ID.
func (e *sftpTestEnv) sftpBackup(configName string) string {
	e.t.Helper()
	snap, err := backup.Run(e.ctx, e.repo, backup.Options{
		Paths:            []string{e.sourceDir},
		ConfigName:       configName,
		Hostname:         "sftp-test-host",
		CompressionLevel: 3,
	})
	if err != nil {
		e.t.Fatalf("backup.Run: %v", err)
	}
	return snap.ID
}

// sftpRestoreTo restores a snapshot to the given directory and returns the
// path where source files ended up (accounting for absolute paths in the tree).
func (e *sftpTestEnv) sftpRestoreTo(snapshotID, targetDir string) string {
	e.t.Helper()
	err := restore.Run(e.ctx, e.repo, snapshotID, targetDir, restore.Options{
		Overwrite: true,
	})
	if err != nil {
		e.t.Fatalf("restore.Run: %v", err)
	}
	return filepath.Join(targetDir, e.sourceDir)
}

// reconnect closes the current backend and connects a new one to the same server.
func (e *sftpTestEnv) reconnect() {
	e.t.Helper()
	e.backend.Close()

	backend, err := sftpBackend.New(e.host, e.port, "testclient", "/repo", e.clientKeyPath, "", "", e.knownHostsCb)
	if err != nil {
		e.t.Fatalf("reconnect sftp.New: %v", err)
	}

	r, err := repo.Open(e.ctx, backend, e.masterKey)
	if err != nil {
		backend.Close()
		e.t.Fatalf("reconnect repo.Open: %v", err)
	}

	e.backend = backend
	e.repo = r
}

// TestSFTPE2EFullRoundtrip tests the complete flow through the built-in SFTP server:
// init repo → backup → check → restore → verify byte-identical.
func TestSFTPE2EFullRoundtrip(t *testing.T) {
	env := newSFTPTestEnv(t)

	// Create source files.
	env.sftpWriteFile("docs/readme.md", "# Hello\nThis is a test file.\n")
	env.sftpWriteFile("docs/notes.txt", "Some notes about the project.\n")
	env.sftpWriteFile("src/main.go", "package main\n\nfunc main() {}\n")
	env.sftpWriteFile("empty.txt", "")
	env.sftpWriteBinaryFile("data/binary.dat", 50000)

	// Backup through SFTP.
	snapID := env.sftpBackup("sftp-test")

	// Check at all levels.
	for _, level := range []check.Level{check.LevelStructure, check.LevelHeaders, check.LevelFull} {
		report, err := check.Run(env.ctx, env.repo, level)
		if err != nil {
			t.Fatalf("check %v: %v", level, err)
		}
		if len(report.Errors) > 0 {
			t.Fatalf("check %v: %d errors: %v", level, len(report.Errors), report.Errors)
		}
	}

	// Restore and compare.
	targetDir := t.TempDir()
	effectiveDir := env.sftpRestoreTo(snapID, targetDir)

	srcFiles := collectFiles(t, env.sourceDir)
	dstFiles := collectFiles(t, effectiveDir)
	assertFilesEqual(t, srcFiles, dstFiles)
}

// TestSFTPE2EIncrementalBackup tests incremental backups through SFTP:
// first backup, modify files, second backup, verify deduplication.
func TestSFTPE2EIncrementalBackup(t *testing.T) {
	env := newSFTPTestEnv(t)

	// First backup.
	env.sftpWriteFile("file1.txt", "original content")
	env.sftpWriteFile("file2.txt", "this won't change")
	snapID1 := env.sftpBackup("incremental-sftp")

	// Modify file1, add file3, leave file2 unchanged.
	env.sftpWriteFile("file1.txt", "modified content")
	env.sftpWriteFile("file3.txt", "brand new file")
	snapID2 := env.sftpBackup("incremental-sftp")

	if snapID1 == snapID2 {
		t.Fatal("expected different snapshot IDs")
	}

	// Restore both snapshots and verify.
	target1 := t.TempDir()
	effective1 := env.sftpRestoreTo(snapID1, target1)
	files1 := collectFiles(t, effective1)

	target2 := t.TempDir()
	effective2 := env.sftpRestoreTo(snapID2, target2)
	files2 := collectFiles(t, effective2)

	// Snap 1: original content.
	sftpAssertFileContent(t, files1, "file1.txt", "original content")
	sftpAssertFileContent(t, files1, "file2.txt", "this won't change")

	// Snap 2: modified content + new file.
	sftpAssertFileContent(t, files2, "file1.txt", "modified content")
	sftpAssertFileContent(t, files2, "file2.txt", "this won't change")
	sftpAssertFileContent(t, files2, "file3.txt", "brand new file")

	// Snap 1 shouldn't have file3.
	if _, ok := sftpFindFile(files1, "file3.txt"); ok {
		t.Error("file3.txt should not exist in snapshot 1")
	}
}

// TestSFTPE2EReopenRepo tests that closing and reopening the SFTP connection
// preserves the repo state.
func TestSFTPE2EReopenRepo(t *testing.T) {
	env := newSFTPTestEnv(t)

	env.sftpWriteFile("persist.txt", "this must survive reconnection")
	snapID := env.sftpBackup("reopen-sftp")

	// Close and reconnect.
	env.reconnect()

	// Verify snapshot exists after reconnect.
	ids, err := env.repo.ListSnapshots(env.ctx)
	if err != nil {
		t.Fatalf("list snapshots after reconnect: %v", err)
	}

	found := false
	for _, id := range ids {
		if id == snapID {
			found = true
		}
	}
	if !found {
		t.Errorf("snapshot %s not found after reconnect", snapID[:10])
	}

	// Restore from the reopened connection.
	targetDir := t.TempDir()
	effectiveDir := env.sftpRestoreTo(snapID, targetDir)

	files := collectFiles(t, effectiveDir)
	sftpAssertFileContent(t, files, "persist.txt", "this must survive reconnection")
}

// TestSFTPE2EMultipleSnapshots tests creating and restoring multiple snapshots
// through SFTP.
func TestSFTPE2EMultipleSnapshots(t *testing.T) {
	env := newSFTPTestEnv(t)

	var snapIDs []string
	for i := 0; i < 5; i++ {
		env.sftpWriteFile(fmt.Sprintf("file_%d.txt", i), fmt.Sprintf("content version %d", i))
		snapIDs = append(snapIDs, env.sftpBackup("multi-sftp"))
	}

	// Each snapshot should be independently restorable.
	for i, id := range snapIDs {
		target := t.TempDir()
		effectiveDir := env.sftpRestoreTo(id, target)
		files := collectFiles(t, effectiveDir)

		// Each snapshot should have files 0..i.
		for j := 0; j <= i; j++ {
			name := fmt.Sprintf("file_%d.txt", j)
			expected := fmt.Sprintf("content version %d", j)
			sftpAssertFileContent(t, files, name, expected)
		}
	}
}

// TestSFTPE2ELargeFile tests backing up and restoring a large file through SFTP.
func TestSFTPE2ELargeFile(t *testing.T) {
	env := newSFTPTestEnv(t)

	// 5 MiB file — large enough to produce multiple chunks.
	env.sftpWriteBinaryFile("large.bin", 5*1024*1024)
	snapID := env.sftpBackup("large-sftp")

	targetDir := t.TempDir()
	effectiveDir := env.sftpRestoreTo(snapID, targetDir)

	srcFiles := collectFiles(t, env.sourceDir)
	dstFiles := collectFiles(t, effectiveDir)
	assertFilesEqual(t, srcFiles, dstFiles)
}

// TestSFTPE2EAuthRejection tests that an unknown SSH key is rejected by the server.
func TestSFTPE2EAuthRejection(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")

	// Generate SSH host key.
	_, hostPrivKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}

	hostKeyPEM, err := ssh.MarshalPrivateKey(hostPrivKey, "")
	if err != nil {
		t.Fatalf("marshal host key: %v", err)
	}

	hostKeyPEMBytes := pem.EncodeToMemory(hostKeyPEM)

	// Generate authorized client key (the one registered with server).
	_, authClientPrivKey, _ := ed25519.GenerateKey(rand.Reader)
	authSigner, _ := ssh.NewSignerFromKey(authClientPrivKey)

	// Generate an UNAUTHORIZED client key (NOT registered with server).
	_, unauthPrivKey, _ := ed25519.GenerateKey(rand.Reader)
	unauthKeyPEM, _ := ssh.MarshalPrivateKey(unauthPrivKey, "")
	unauthKeyPath := filepath.Join(t.TempDir(), "unauth_key")
	os.WriteFile(unauthKeyPath, pem.EncodeToMemory(unauthKeyPEM), 0600)

	dataDir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverCfg := server.Config{
		HostKeyPEM: hostKeyPEMBytes,
		DataDir:    dataDir,
		Clients: []server.ClientConfig{
			{
				Name:      "authorized",
				PublicKey: authSigner.PublicKey(),
			},
		},
		Listener: ln,
	}

	go server.Start(ctx, serverCfg)
	time.Sleep(50 * time.Millisecond)

	// Write known_hosts.
	hostPubKey, _ := ssh.NewPublicKey(hostPrivKey.Public())
	addr := ln.Addr().String()
	host, port, _ := net.SplitHostPort(addr)
	knownHostLine := fmt.Sprintf("[%s]:%s %s", host, port, string(ssh.MarshalAuthorizedKey(hostPubKey)))
	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	os.WriteFile(knownHostsPath, []byte(knownHostLine), 0600)

	hostKeyCallback, err := sftpBackend.HostKeyFile(knownHostsPath)
	if err != nil {
		t.Fatalf("host key callback: %v", err)
	}

	// Try connecting with the unauthorized key — should fail.
	_, err = sftpBackend.New(host, port, "intruder", "/repo", unauthKeyPath, "", "", hostKeyCallback)
	if err == nil {
		t.Fatal("expected connection with unauthorized key to fail")
	}
}

// TestSFTPE2EClientIsolation tests that two clients on the same server
// cannot see each other's data.
func TestSFTPE2EClientIsolation(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")

	// Generate keys.
	_, hostPrivKey, _ := ed25519.GenerateKey(rand.Reader)
	hostKeyPEM, _ := ssh.MarshalPrivateKey(hostPrivKey, "")
	hostKeyPEMBytes := pem.EncodeToMemory(hostKeyPEM)

	_, client1PrivKey, _ := ed25519.GenerateKey(rand.Reader)
	client1Signer, _ := ssh.NewSignerFromKey(client1PrivKey)
	client1KeyPEM, _ := ssh.MarshalPrivateKey(client1PrivKey, "")
	client1KeyPath := filepath.Join(t.TempDir(), "client1_key")
	os.WriteFile(client1KeyPath, pem.EncodeToMemory(client1KeyPEM), 0600)

	_, client2PrivKey, _ := ed25519.GenerateKey(rand.Reader)
	client2Signer, _ := ssh.NewSignerFromKey(client2PrivKey)
	client2KeyPEM, _ := ssh.MarshalPrivateKey(client2PrivKey, "")
	client2KeyPath := filepath.Join(t.TempDir(), "client2_key")
	os.WriteFile(client2KeyPath, pem.EncodeToMemory(client2KeyPEM), 0600)

	dataDir := t.TempDir()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverCfg := server.Config{
		HostKeyPEM: hostKeyPEMBytes,
		DataDir:    dataDir,
		Clients: []server.ClientConfig{
			{Name: "client1", PublicKey: client1Signer.PublicKey()},
			{Name: "client2", PublicKey: client2Signer.PublicKey()},
		},
		Listener: ln,
	}

	go server.Start(ctx, serverCfg)
	time.Sleep(50 * time.Millisecond)

	// Known hosts.
	hostPubKey, _ := ssh.NewPublicKey(hostPrivKey.Public())
	addr := ln.Addr().String()
	host, port, _ := net.SplitHostPort(addr)
	knownHostLine := fmt.Sprintf("[%s]:%s %s", host, port, string(ssh.MarshalAuthorizedKey(hostPubKey)))
	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	os.WriteFile(knownHostsPath, []byte(knownHostLine), 0600)
	hostKeyCallback, _ := sftpBackend.HostKeyFile(knownHostsPath)

	// Connect client 1 and init a repo.
	backend1, err := sftpBackend.New(host, port, "client1", "/repo", client1KeyPath, "", "", hostKeyCallback)
	if err != nil {
		t.Fatalf("client1 connect: %v", err)
	}
	defer backend1.Close()

	var mk1 crypto.MasterKey
	rand.Read(mk1[:])
	repo1, err := repo.Init(ctx, backend1, mk1)
	if err != nil {
		t.Fatalf("client1 repo.Init: %v", err)
	}

	// Connect client 2 and init a separate repo.
	backend2, err := sftpBackend.New(host, port, "client2", "/repo", client2KeyPath, "", "", hostKeyCallback)
	if err != nil {
		t.Fatalf("client2 connect: %v", err)
	}
	defer backend2.Close()

	var mk2 crypto.MasterKey
	rand.Read(mk2[:])
	repo2, err := repo.Init(ctx, backend2, mk2)
	if err != nil {
		t.Fatalf("client2 repo.Init: %v", err)
	}

	// Create source files for each client.
	src1 := t.TempDir()
	os.WriteFile(filepath.Join(src1, "client1.txt"), []byte("client 1 data"), 0644)

	src2 := t.TempDir()
	os.WriteFile(filepath.Join(src2, "client2.txt"), []byte("client 2 data"), 0644)

	// Backup each client's data.
	snap1, err := backup.Run(ctx, repo1, backup.Options{
		Paths:            []string{src1},
		ConfigName:       "c1-backup",
		Hostname:         "host1",
		CompressionLevel: 3,
	})
	if err != nil {
		t.Fatalf("client1 backup: %v", err)
	}

	snap2, err := backup.Run(ctx, repo2, backup.Options{
		Paths:            []string{src2},
		ConfigName:       "c2-backup",
		Hostname:         "host2",
		CompressionLevel: 3,
	})
	if err != nil {
		t.Fatalf("client2 backup: %v", err)
	}

	// Verify each client only sees their own snapshots.
	ids1, _ := repo1.ListSnapshots(ctx)
	ids2, _ := repo2.ListSnapshots(ctx)

	if len(ids1) != 1 || ids1[0] != snap1.ID {
		t.Errorf("client1 should see exactly 1 snapshot (theirs), got %d", len(ids1))
	}
	if len(ids2) != 1 || ids2[0] != snap2.ID {
		t.Errorf("client2 should see exactly 1 snapshot (theirs), got %d", len(ids2))
	}

	// Restore and verify isolation.
	target1 := t.TempDir()
	restore.Run(ctx, repo1, snap1.ID, target1, restore.Options{Overwrite: true})
	files1 := collectFiles(t, filepath.Join(target1, src1))

	target2 := t.TempDir()
	restore.Run(ctx, repo2, snap2.ID, target2, restore.Options{Overwrite: true})
	files2 := collectFiles(t, filepath.Join(target2, src2))

	sftpAssertFileContent(t, files1, "client1.txt", "client 1 data")
	sftpAssertFileContent(t, files2, "client2.txt", "client 2 data")

	// Client 1 shouldn't have client 2's file and vice versa.
	if _, ok := sftpFindFile(files1, "client2.txt"); ok {
		t.Error("client1 should not see client2's data")
	}
	if _, ok := sftpFindFile(files2, "client1.txt"); ok {
		t.Error("client2 should not see client1's data")
	}
}

// --- SFTP-specific helpers (prefixed to avoid conflicts with e2e_test.go) ---

// sftpAssertFileContent checks that a file in the collected map has the expected content.
func sftpAssertFileContent(t *testing.T, files map[string][]byte, name, expected string) {
	t.Helper()
	content, ok := sftpFindFile(files, name)
	if !ok {
		t.Errorf("file %q not found in restored files", name)
		return
	}
	if string(content) != expected {
		t.Errorf("file %q: got %q, want %q", name, content, expected)
	}
}

// sftpFindFile looks for a file by name (basename match) in the collected files map.
func sftpFindFile(files map[string][]byte, name string) ([]byte, bool) {
	for path, content := range files {
		if filepath.Base(path) == name {
			return content, true
		}
	}
	return nil, false
}
