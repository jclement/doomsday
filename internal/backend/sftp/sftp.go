// Package sftp implements a Backend backed by an SFTP remote server.
package sftp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"sort"
	"time"

	"github.com/jclement/doomsday/internal/types"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Backend stores repository data on a remote SFTP server.
type Backend struct {
	basePath   string
	host       string
	port       string
	user       string
	sshClient  *ssh.Client
	sftpClient *sftp.Client
	agentConn  net.Conn // SSH agent connection, closed on Backend.Close()
}

// New connects to the SFTP server and returns a Backend rooted at basePath.
//
// Authentication is attempted in order: inline SSH key (sshKey), SSH agent,
// public key file, password. At least one must be available.
//
// hostKeyCallback can be obtained from HostKeyFingerprint() or HostKeyFile().
//
// The basePath is the remote directory that serves as the repository root.
// The directory structure is created if it doesn't already exist.
func New(host, port, user, basePath, keyFile, password, sshKey string, hostKeyCallback ssh.HostKeyCallback) (*Backend, error) {
	if port == "" {
		port = "22"
	}
	if hostKeyCallback == nil {
		return nil, fmt.Errorf("sftp.New: host key callback is required (use sftp.HostKeyFingerprint or sftp.HostKeyFile)")
	}

	authMethods, agentConn, err := buildAuthMethods(sshKey, keyFile, password)
	if err != nil {
		return nil, fmt.Errorf("sftp.New: %w", err)
	}
	if len(authMethods) == 0 {
		if agentConn != nil {
			agentConn.Close()
		}
		return nil, fmt.Errorf("sftp.New: no authentication methods available")
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         30 * time.Second,
	}

	addr := net.JoinHostPort(host, port)
	sshClient, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("sftp.New: ssh dial %s: %w", addr, err)
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		if agentConn != nil {
			agentConn.Close()
		}
		return nil, fmt.Errorf("sftp.New: sftp client: %w", err)
	}

	b := &Backend{
		basePath:   basePath,
		host:       host,
		port:       port,
		user:       user,
		sshClient:  sshClient,
		sftpClient: sftpClient,
		agentConn:  agentConn,
	}

	// Create repository directory structure.
	for _, ft := range []types.FileType{
		types.FileTypePack, types.FileTypeIndex, types.FileTypeSnapshot,
		types.FileTypeKey, types.FileTypeLock,
	} {
		dir := b.dir(ft)
		if err := sftpClient.MkdirAll(dir); err != nil {
			b.Close()
			return nil, fmt.Errorf("sftp.New: create %s: %w", dir, err)
		}
	}

	return b, nil
}

// NewFromClients creates a Backend using pre-established SSH and SFTP clients.
// This is primarily useful for testing.
func NewFromClients(sshClient *ssh.Client, sftpClient *sftp.Client, host, port, user, basePath string) (*Backend, error) {
	b := &Backend{
		basePath:   basePath,
		host:       host,
		port:       port,
		user:       user,
		sshClient:  sshClient,
		sftpClient: sftpClient,
	}

	for _, ft := range []types.FileType{
		types.FileTypePack, types.FileTypeIndex, types.FileTypeSnapshot,
		types.FileTypeKey, types.FileTypeLock,
	} {
		dir := b.dir(ft)
		if err := sftpClient.MkdirAll(dir); err != nil {
			return nil, fmt.Errorf("sftp.NewFromClients: create %s: %w", dir, err)
		}
	}

	return b, nil
}

func (b *Backend) Location() string {
	return fmt.Sprintf("sftp:%s@%s:%s", b.user, net.JoinHostPort(b.host, b.port), b.basePath)
}

func (b *Backend) Save(_ context.Context, t types.FileType, name string, rd io.Reader) error {
	if err := types.ValidateName(name); err != nil {
		return fmt.Errorf("sftp.Save: %w", err)
	}
	filePath := b.path(t, name)

	// Ensure parent directory exists (for pack files with hex prefix subdirs).
	parentDir := path.Dir(filePath)
	if err := b.sftpClient.MkdirAll(parentDir); err != nil {
		return fmt.Errorf("sftp.Save: mkdir %s: %w", parentDir, err)
	}

	// Atomic write: write to temp file with random suffix, then rename.
	// Using a unique suffix avoids needing to remove stale .tmp files and
	// avoids O_TRUNC which append-only servers (like doomsday server) reject.
	var rndBuf [4]byte
	if _, err := rand.Read(rndBuf[:]); err != nil {
		return fmt.Errorf("sftp.Save: random: %w", err)
	}
	tmpPath := filePath + ".tmp." + hex.EncodeToString(rndBuf[:])
	f, err := b.sftpClient.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return fmt.Errorf("sftp.Save: create temp: %w", err)
	}

	if _, err := io.Copy(f, rd); err != nil {
		f.Close()
		b.sftpClient.Remove(tmpPath)
		return fmt.Errorf("sftp.Save: write: %w", err)
	}

	if err := f.Close(); err != nil {
		b.sftpClient.Remove(tmpPath)
		return fmt.Errorf("sftp.Save: close: %w", err)
	}

	// Rename is atomic on most SFTP servers (POSIX semantics).
	if err := b.sftpClient.Rename(tmpPath, filePath); err != nil {
		// Some servers fail rename if target exists; remove target and retry.
		b.sftpClient.Remove(filePath)
		if err := b.sftpClient.Rename(tmpPath, filePath); err != nil {
			b.sftpClient.Remove(tmpPath)
			return fmt.Errorf("sftp.Save: rename: %w", err)
		}
	}

	return nil
}

func (b *Backend) Load(_ context.Context, t types.FileType, name string, offset, length int64) (io.ReadCloser, error) {
	if err := types.ValidateName(name); err != nil {
		return nil, fmt.Errorf("sftp.Load: %w", err)
	}
	filePath := b.path(t, name)
	f, err := b.sftpClient.Open(filePath)
	if err != nil {
		if isNotExist(err) {
			return nil, fmt.Errorf("sftp.Load: %w", types.ErrNotFound)
		}
		return nil, fmt.Errorf("sftp.Load: %w", err)
	}

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			return nil, fmt.Errorf("sftp.Load: seek: %w", err)
		}
	}

	if length > 0 {
		return &limitedReadCloser{Reader: io.LimitReader(f, length), Closer: f}, nil
	}

	return f, nil
}

func (b *Backend) Stat(_ context.Context, t types.FileType, name string) (types.FileInfo, error) {
	if err := types.ValidateName(name); err != nil {
		return types.FileInfo{}, fmt.Errorf("sftp.Stat: %w", err)
	}
	filePath := b.path(t, name)
	info, err := b.sftpClient.Stat(filePath)
	if err != nil {
		if isNotExist(err) {
			return types.FileInfo{}, fmt.Errorf("sftp.Stat: %w", types.ErrNotFound)
		}
		return types.FileInfo{}, fmt.Errorf("sftp.Stat: %w", err)
	}
	return types.FileInfo{Name: name, Size: info.Size()}, nil
}

func (b *Backend) Remove(_ context.Context, t types.FileType, name string) error {
	if err := types.ValidateName(name); err != nil {
		return fmt.Errorf("sftp.Remove: %w", err)
	}
	filePath := b.path(t, name)
	if err := b.sftpClient.Remove(filePath); err != nil {
		if isNotExist(err) {
			return nil // idempotent
		}
		return fmt.Errorf("sftp.Remove: %w", err)
	}
	return nil
}

func (b *Backend) List(_ context.Context, t types.FileType, fn func(types.FileInfo) error) error {
	if t == types.FileTypeConfig {
		// Config is a single file, not a directory.
		p := b.path(t, "")
		info, err := b.sftpClient.Stat(p)
		if err != nil {
			if isNotExist(err) {
				return nil
			}
			return fmt.Errorf("sftp.List: %w", err)
		}
		return fn(types.FileInfo{Name: "config", Size: info.Size()})
	}

	dir := b.dir(t)

	if t == types.FileTypePack {
		return b.listPackFiles(dir, fn)
	}

	entries, err := b.sftpClient.ReadDir(dir)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return fmt.Errorf("sftp.List: %w", err)
	}

	// Sort for deterministic ordering.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := fn(types.FileInfo{Name: entry.Name(), Size: entry.Size()}); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) listPackFiles(dir string, fn func(types.FileInfo) error) error {
	prefixes, err := b.sftpClient.ReadDir(dir)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return fmt.Errorf("sftp.List: %w", err)
	}

	// Sort prefixes for deterministic ordering.
	sort.Slice(prefixes, func(i, j int) bool {
		return prefixes[i].Name() < prefixes[j].Name()
	})

	for _, prefix := range prefixes {
		if !prefix.IsDir() {
			continue
		}
		subdir := path.Join(dir, prefix.Name())
		entries, err := b.sftpClient.ReadDir(subdir)
		if err != nil {
			continue
		}

		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			// Return plain filename (without prefix dir) for consistency
			// with Save/Load which accept plain names.
			if err := fn(types.FileInfo{Name: entry.Name(), Size: entry.Size()}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *Backend) Close() error {
	var firstErr error
	if b.sftpClient != nil {
		if err := b.sftpClient.Close(); err != nil {
			firstErr = err
		}
	}
	if b.sshClient != nil {
		if err := b.sshClient.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if b.agentConn != nil {
		if err := b.agentConn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return fmt.Errorf("sftp.Close: %w", firstErr)
	}
	return nil
}

// dir returns the remote directory path for a file type.
func (b *Backend) dir(t types.FileType) string {
	return path.Join(b.basePath, t.String())
}

// path returns the full remote file path for a file type and name.
func (b *Backend) path(t types.FileType, name string) string {
	if t == types.FileTypePack && len(name) >= 2 {
		return path.Join(b.basePath, t.String(), name[:2], name)
	}
	if t == types.FileTypeConfig {
		return path.Join(b.basePath, "config")
	}
	return path.Join(b.basePath, t.String(), name)
}

// buildAuthMethods constructs SSH authentication methods from the given credentials.
// Returns the auth methods and the agent connection (if any) so the caller can close it.
//
// sshKey is a base64url-encoded Ed25519 private key seed (32 bytes, from server one-liner).
func buildAuthMethods(sshKey, keyFile, password string) ([]ssh.AuthMethod, net.Conn, error) {
	var methods []ssh.AuthMethod
	var agentConn net.Conn

	// Inline SSH key (base64-encoded Ed25519 seed from server one-liner).
	if sshKey != "" {
		signer, err := signerFromSeed(sshKey)
		if err != nil {
			return nil, nil, fmt.Errorf("parse inline ssh_key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	// Try SSH agent.
	if conn := sshAgentConn(); conn != nil {
		agentConn = conn
		methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
	}

	// Public key file.
	if keyFile != "" {
		keyData, err := os.ReadFile(keyFile)
		if err != nil {
			if agentConn != nil {
				agentConn.Close()
			}
			return nil, nil, fmt.Errorf("read key file %s: %w", keyFile, err)
		}
		signer, err := ssh.ParsePrivateKey(keyData)
		if err != nil {
			if agentConn != nil {
				agentConn.Close()
			}
			return nil, nil, fmt.Errorf("parse key file %s: %w", keyFile, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	// Password.
	if password != "" {
		methods = append(methods, ssh.Password(password))
	}

	return methods, agentConn, nil
}

// sshAgentConn attempts to connect to the SSH agent socket.
// Returns nil if the agent is not available.
func sshAgentConn() net.Conn {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil
	}
	return conn
}

// isNotExist returns true if the error indicates a file does not exist.
func isNotExist(err error) bool {
	if os.IsNotExist(err) {
		return true
	}
	// pkg/sftp returns *StatusError for SFTP-level errors.
	if sftpErr, ok := err.(*sftp.StatusError); ok {
		return sftpErr.Code == ssh_FX_NO_SUCH_FILE
	}
	return false
}

// ssh_FX_NO_SUCH_FILE is the SFTP status code for "no such file".
const ssh_FX_NO_SUCH_FILE = 2

// limitedReadCloser wraps an io.Reader and io.Closer.
type limitedReadCloser struct {
	Reader io.Reader
	Closer io.Closer
}

func (l *limitedReadCloser) Read(p []byte) (int, error) { return l.Reader.Read(p) }
func (l *limitedReadCloser) Close() error                { return l.Closer.Close() }

// Ensure Backend implements types.Backend at compile time.
var _ types.Backend = (*Backend)(nil)

// HostKeyFile returns a host key callback that validates against a known_hosts file.
func HostKeyFile(file string) (ssh.HostKeyCallback, error) {
	cb, err := knownhosts.New(file)
	if err != nil {
		return nil, fmt.Errorf("sftp.HostKeyFile: %w", err)
	}
	return cb, nil
}

// HostKeyFingerprint returns a host key callback that validates against a pinned
// SHA256 fingerprint (e.g. "SHA256:abc123...").
func HostKeyFingerprint(fingerprint string) (ssh.HostKeyCallback, error) {
	if fingerprint == "" {
		return nil, fmt.Errorf("sftp.HostKeyFingerprint: empty fingerprint")
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		actual := ssh.FingerprintSHA256(key)
		if actual != fingerprint {
			return fmt.Errorf("host key mismatch for %s: expected %s, got %s", hostname, fingerprint, actual)
		}
		return nil
	}, nil
}

// signerFromSeed creates an SSH signer from a base64-encoded Ed25519 seed (32 bytes).
func signerFromSeed(b64Seed string) (ssh.Signer, error) {
	seed, err := base64.RawURLEncoding.DecodeString(b64Seed)
	if err != nil {
		// Try standard base64 as fallback.
		seed, err = base64.StdEncoding.DecodeString(b64Seed)
		if err != nil {
			return nil, fmt.Errorf("decode ssh key seed: %w", err)
		}
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("ssh key seed has wrong length: got %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	key := ed25519.NewKeyFromSeed(seed)
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, fmt.Errorf("create signer from ed25519 key: %w", err)
	}
	return signer, nil
}
