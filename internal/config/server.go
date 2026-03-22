package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultServerConfigPath returns the default path for server.yaml.
func DefaultServerConfigPath() string {
	return filepath.Join(DefaultConfigDir(), "server.yaml")
}

// ServerConfig is the top-level doomsday server configuration.
type ServerConfig struct {
	DataDir          string               `yaml:"data_dir"`                    // root directory for client data
	Host             string               `yaml:"host,omitempty"`              // listen address (default 0.0.0.0)
	Port             int                  `yaml:"port,omitempty"`              // listen port (default 8420)
	HostKey          string               `yaml:"host_key,omitempty"`          // SSH host private key (PEM, auto-generated)
	TailscaleHostname string              `yaml:"tailscale_hostname,omitempty"` // Tailscale hostname (presence enables Tailscale)
	TailscaleAuthKey  string              `yaml:"tailscale_auth_key,omitempty"` // Tailscale auth key for headless setup
	Clients          []ServerClientConfig `yaml:"clients,omitempty"`           // registered clients
}

// TailscaleEnabled returns true if Tailscale is configured (hostname is set).
func (c *ServerConfig) TailscaleEnabled() bool {
	return c.TailscaleHostname != ""
}

// ServerClientConfig defines a registered backup client.
type ServerClientConfig struct {
	Name       string `yaml:"name"`                 // client username
	PublicKey  string `yaml:"public_key"`            // SSH public key (authorized_keys format)
	Quota      string `yaml:"quota,omitempty"`       // e.g. "100GiB", "" or "0" = unlimited
	AppendOnly bool   `yaml:"append_only,omitempty"` // restrict to append-only operations
}

// LoadServer reads and parses a server YAML config file.
func LoadServer(path string) (*ServerConfig, error) {
	path = ExpandPath(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config.LoadServer: %w", err)
	}

	return ParseServer(data)
}

// ParseServer parses server YAML configuration data from bytes.
func ParseServer(data []byte) (*ServerConfig, error) {
	var cfg ServerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config.ParseServer: %w", err)
	}

	applyServerDefaults(&cfg)
	return &cfg, nil
}

// applyServerDefaults fills in default values for server config.
func applyServerDefaults(cfg *ServerConfig) {
	if cfg.Host == "" {
		cfg.Host = "0.0.0.0"
	}
	if cfg.Port == 0 {
		cfg.Port = 8420
	}
}

// ValidateServer checks the server configuration for required fields.
func (c *ServerConfig) Validate() []error {
	var errs []error

	if c.DataDir == "" {
		errs = append(errs, fmt.Errorf("config.ValidateServer: data_dir is required"))
	}

	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, fmt.Errorf("config.ValidateServer: port must be 1-65535, got %d", c.Port))
	}

	seenNames := make(map[string]bool)
	for i, cl := range c.Clients {
		if cl.Name == "" {
			errs = append(errs, fmt.Errorf("config.ValidateServer: client[%d].name is required", i))
		} else if seenNames[cl.Name] {
			errs = append(errs, fmt.Errorf("config.ValidateServer: duplicate client name %q", cl.Name))
		} else {
			seenNames[cl.Name] = true
		}

		if cl.PublicKey == "" {
			errs = append(errs, fmt.Errorf("config.ValidateServer: client[%d] (%q).public_key is required", i, cl.Name))
		}
	}

	return errs
}

// FindClient returns the client config with the given name.
func (c *ServerConfig) FindClient(name string) (*ServerClientConfig, error) {
	for i := range c.Clients {
		if c.Clients[i].Name == name {
			return &c.Clients[i], nil
		}
	}
	return nil, fmt.Errorf("config.FindClient: client %q not found", name)
}

// AddClient adds a client to the config. Returns error if name already exists.
func (c *ServerConfig) AddClient(client ServerClientConfig) error {
	for _, existing := range c.Clients {
		if existing.Name == client.Name {
			return fmt.Errorf("config.AddClient: client %q already exists", client.Name)
		}
	}
	c.Clients = append(c.Clients, client)
	return nil
}

// RemoveClient removes a client by name. Returns error if not found.
func (c *ServerConfig) RemoveClient(name string) error {
	for i, cl := range c.Clients {
		if cl.Name == name {
			c.Clients = append(c.Clients[:i], c.Clients[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("config.RemoveClient: client %q not found", name)
}

// SaveServer writes the server config to a YAML file with inline documentation.
func SaveServer(path string, cfg *ServerConfig) error {
	path = ExpandPath(path)

	content := RenderServerConfig(cfg)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("config.SaveServer: %w", err)
	}
	return nil
}

// RenderServerConfig produces a well-documented server.yaml with inline comments.
// Every save produces consistent documentation so the file is always self-explanatory.
func RenderServerConfig(cfg *ServerConfig) string {
	var b strings.Builder

	b.WriteString("# Doomsday Server — SFTP backup server\n")
	b.WriteString("# https://github.com/jclement/doomsday\n\n")

	// ── Data Directory ──
	b.WriteString("# ── Data Directory ───────────────────────────────────\n")
	b.WriteString("# Root directory where client backup data is stored.\n")
	b.WriteString("# Each client gets a subdirectory: <data_dir>/<client_name>/\n")
	fmt.Fprintf(&b, "data_dir: %s\n\n", cfg.DataDir)

	// ── Network ──
	b.WriteString("# ── Network ──────────────────────────────────────────\n")
	b.WriteString("# Listen address and port for incoming SSH/SFTP connections.\n")
	b.WriteString("# Use 0.0.0.0 to listen on all interfaces, or 127.0.0.1 for local only.\n")
	b.WriteString("# Note: when Tailscale is enabled, host is ignored (listens on tailnet only).\n")
	fmt.Fprintf(&b, "host: %s\n", cfg.Host)
	fmt.Fprintf(&b, "port: %d\n\n", cfg.Port)

	// ── Tailscale ──
	b.WriteString("# ── Tailscale (optional) ────────────────────────────\n")
	b.WriteString("# Expose the server on your Tailnet via tsnet. When enabled, the server\n")
	b.WriteString("# joins your tailnet as a node — no firewall rules or port forwarding needed.\n")
	b.WriteString("# Use the full tailnet FQDN (e.g. doomsday.tail1234.ts.net).\n")
	b.WriteString("# Find yours at: https://login.tailscale.com/admin/machines\n")
	b.WriteString("# Set tailscale_hostname to enable (presence = enabled).\n")
	if cfg.TailscaleHostname != "" {
		fmt.Fprintf(&b, "tailscale_hostname: %s\n", cfg.TailscaleHostname)
	} else {
		b.WriteString("# tailscale_hostname: doomsday.tail1234.ts.net\n")
	}
	b.WriteString("#\n")
	b.WriteString("# For headless/unattended setup (servers, VMs, containers), provide an\n")
	b.WriteString("# auth key so the node can join without browser login:\n")
	b.WriteString("#   Generate at: https://login.tailscale.com/admin/settings/keys\n")
	if cfg.TailscaleAuthKey != "" {
		fmt.Fprintf(&b, "tailscale_auth_key: %s\n", cfg.TailscaleAuthKey)
	} else {
		b.WriteString("# tailscale_auth_key: tskey-auth-xxxx\n")
	}
	b.WriteString("\n")

	// ── SSH Host Key ──
	b.WriteString("# ── SSH Host Key ─────────────────────────────────────\n")
	b.WriteString("# Auto-generated Ed25519 host key. Clients pin the fingerprint\n")
	b.WriteString("# via the host_key field in their destination config.\n")
	b.WriteString("# Do not modify unless you know what you're doing.\n")
	if cfg.HostKey != "" {
		b.WriteString("host_key: |\n")
		for _, line := range strings.Split(strings.TrimSpace(cfg.HostKey), "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	} else {
		b.WriteString("# host_key: (auto-generated on first serve)\n")
	}
	b.WriteString("\n")

	// ── Clients ──
	b.WriteString("# ── Clients ──────────────────────────────────────────\n")
	b.WriteString("# Registered backup clients. Manage with:\n")
	b.WriteString("#   doomsday server client add <name>       # generate keypair & register\n")
	b.WriteString("#   doomsday server client remove <name>    # unregister (data preserved)\n")
	b.WriteString("#   doomsday server client list             # show all clients\n")
	b.WriteString("#\n")
	b.WriteString("# Fields:\n")
	b.WriteString("#   name:        unique client name (also the data subdirectory name)\n")
	b.WriteString("#   public_key:  SSH public key in authorized_keys format\n")
	b.WriteString("#   quota:       storage limit — e.g. \"50GiB\", \"1TiB\" (default: unlimited)\n")
	b.WriteString("#   append_only: if true, client cannot delete or overwrite (default: false)\n")

	if len(cfg.Clients) == 0 {
		b.WriteString("#\n")
		b.WriteString("# clients:\n")
		b.WriteString("#   - name: laptop\n")
		b.WriteString("#     public_key: \"ssh-ed25519 AAAA...\"\n")
		b.WriteString("#     quota: 100GiB\n")
		b.WriteString("#   - name: server-prod\n")
		b.WriteString("#     public_key: \"ssh-ed25519 BBBB...\"\n")
		b.WriteString("#     append_only: true\n")
	} else {
		b.WriteString("clients:\n")
		for _, cl := range cfg.Clients {
			fmt.Fprintf(&b, "  - name: %s\n", cl.Name)
			fmt.Fprintf(&b, "    public_key: %q\n", cl.PublicKey)
			if cl.Quota != "" {
				fmt.Fprintf(&b, "    quota: %s\n", cl.Quota)
			}
			if cl.AppendOnly {
				b.WriteString("    append_only: true\n")
			}
		}
	}

	return b.String()
}
