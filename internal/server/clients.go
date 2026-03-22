package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"golang.org/x/crypto/ssh"
)

// validClientName matches alphanumeric characters, hyphens, and underscores.
// Prevents path traversal via client names used as filesystem directory names.
var validClientName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

var (
	// ErrClientExists is returned when adding a client that already exists.
	ErrClientExists = errors.New("server: client already exists")

	// ErrClientNotFound is returned when operating on a client that does not exist.
	ErrClientNotFound = errors.New("server: client not found")
)

// ClientConfig describes an authorized client.
type ClientConfig struct {
	// Name is the unique client identifier. Also used as the subdirectory name.
	Name string

	// PublicKey is the parsed SSH public key for authentication.
	PublicKey ssh.PublicKey

	// QuotaBytes is the maximum number of bytes this client may store (0 = unlimited).
	QuotaBytes int64

	// AppendOnly restricts the client to append-only operations (no overwrite/truncate/delete).
	AppendOnly bool
}

// ClientManager handles client lookup at runtime.
// Clients are loaded from server.yaml at startup; this is an in-memory index.
type ClientManager struct {
	mu      sync.RWMutex
	dataDir string
	clients map[string]*ClientConfig
}

// NewClientManager creates a ClientManager from the given client configs.
// It creates jail directories for each client under dataDir.
func NewClientManager(dataDir string, clients []ClientConfig) (*ClientManager, error) {
	cm := &ClientManager{
		dataDir: dataDir,
		clients: make(map[string]*ClientConfig),
	}

	for i := range clients {
		cc := clients[i]

		// SECURITY: Validate client name to prevent path traversal.
		if !validClientName.MatchString(cc.Name) {
			return nil, fmt.Errorf("server: invalid client name %q: must be alphanumeric with hyphens/underscores", cc.Name)
		}

		if _, exists := cm.clients[cc.Name]; exists {
			return nil, fmt.Errorf("server: duplicate client name %q", cc.Name)
		}

		// Create client jail directory.
		clientDir := filepath.Join(dataDir, cc.Name)
		if err := os.MkdirAll(clientDir, 0700); err != nil {
			return nil, fmt.Errorf("create client dir %q: %w", cc.Name, err)
		}

		cm.clients[cc.Name] = &cc
	}

	return cm, nil
}

// Get returns a copy of the ClientConfig for the named client.
func (cm *ClientManager) Get(name string) (ClientConfig, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	cc, ok := cm.clients[name]
	if !ok {
		return ClientConfig{}, false
	}
	return *cc, true
}

// List returns all registered clients.
func (cm *ClientManager) List() []ClientConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make([]ClientConfig, 0, len(cm.clients))
	for _, cc := range cm.clients {
		result = append(result, *cc)
	}
	return result
}

// Replace atomically swaps the client set. New jail directories are created;
// removed clients keep their data on disk but are no longer authorized.
func (cm *ClientManager) Replace(clients []ClientConfig) error {
	newMap := make(map[string]*ClientConfig, len(clients))
	for i := range clients {
		cc := clients[i]
		if !validClientName.MatchString(cc.Name) {
			return fmt.Errorf("server: invalid client name %q", cc.Name)
		}
		if _, exists := newMap[cc.Name]; exists {
			return fmt.Errorf("server: duplicate client name %q", cc.Name)
		}
		clientDir := filepath.Join(cm.dataDir, cc.Name)
		if err := os.MkdirAll(clientDir, 0700); err != nil {
			return fmt.Errorf("create client dir %q: %w", cc.Name, err)
		}
		newMap[cc.Name] = &cc
	}

	cm.mu.Lock()
	cm.clients = newMap
	cm.mu.Unlock()
	return nil
}

// FindByKey looks up a client by their SSH public key.
func (cm *ClientManager) FindByKey(key ssh.PublicKey) (ClientConfig, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	keyBytes := key.Marshal()

	for _, cc := range cm.clients {
		if cc.PublicKey != nil && string(cc.PublicKey.Marshal()) == string(keyBytes) {
			return *cc, true
		}
	}
	return ClientConfig{}, false
}
