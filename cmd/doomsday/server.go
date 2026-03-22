package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jclement/doomsday/internal/config"
	"github.com/jclement/doomsday/internal/server"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"tailscale.com/tsnet"
)

// ---------------------------------------------------------------------------
// Flag variables
// ---------------------------------------------------------------------------

var (
	serverFlagConfig   string // --config for server.yaml path
	serverFlagPort     int
	serverFlagHost     string
	serverFlagDataDir  string // only for install (overrides config)
	serverFlagQuota    string
	serverFlagAppendOnly bool
)

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run and manage the SFTP backup server",
	Long: `Run the doomsday SFTP backup server, or manage its installation and clients.

When invoked without a subcommand, shows server status.

Examples:
  doomsday server
  doomsday server serve
  doomsday server client add laptop --quota 100GiB
  doomsday server client list`,
	RunE: runServerStatus,
}

var serverInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a new server configuration",
	Long: `Create a new server.yaml configuration file with sensible defaults.
Generates an SSH host key if one doesn't exist.

Examples:
  doomsday server init
  doomsday server init --config /etc/doomsday/server.yaml`,
	RunE: runServerInit,
}

var serverServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the SFTP backup server",
	Long: `Start the doomsday SFTP backup server. Loads configuration from server.yaml
and listens for incoming SSH/SFTP connections.

Examples:
  doomsday server serve
  doomsday server serve --config /etc/doomsday/server.yaml`,
	RunE: runServerServe,
}

var serverInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the server as a system daemon",
	Long: `Install the doomsday SFTP server as a system-level daemon.

On Linux: creates a systemd unit, dedicated user, data directory.
On macOS: creates a launchd plist, dedicated user, data directory.
Requires root/sudo.

Examples:
  sudo doomsday server install`,
	RunE: runServerInstall,
}

var serverUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the installed server daemon",
	Long: `Stop and remove the doomsday server daemon. Does NOT delete the data
directory to preserve backup data.

Examples:
  sudo doomsday server uninstall`,
	RunE: runServerUninstall,
}

var serverStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show server daemon status",
	RunE:  runServerStatus,
}

// Client management parent command.
var serverClientCmd = &cobra.Command{
	Use:   "client",
	Short: "Manage backup clients",
	Long: `Add, remove, or list registered backup clients.

When adding a client, an Ed25519 keypair is generated automatically.
The private key is printed as a one-liner for the client to use.`,
}

var serverClientAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Register a new backup client",
	Long: `Generate an Ed25519 keypair for the client, store the public key in
server.yaml, and print a one-liner command for the client.

Examples:
  doomsday server client add laptop
  doomsday server client add laptop --quota 100GiB
  doomsday server client add laptop --append-only`,
	Args: exactArgs(1),
	RunE: runServerClientAdd,
}

var serverClientRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Unregister a backup client",
	Long: `Remove a registered backup client from server.yaml. The client's data
directory is preserved (not deleted).

Examples:
  doomsday server client remove laptop`,
	Args: exactArgs(1),
	RunE: runServerClientRemove,
}

var serverClientListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered backup clients",
	RunE:  runServerClientList,
}

func init() {
	// Persistent flag for server config path.
	serverCmd.PersistentFlags().StringVarP(&serverFlagConfig, "config", "c", "", "server config path (default: ~/.config/doomsday/server.yaml)")

	// Serve flags.
	serverServeCmd.Flags().IntVar(&serverFlagPort, "port", 0, "override listen port from config")
	serverServeCmd.Flags().StringVar(&serverFlagHost, "host", "", "override listen host from config")

	// Install flags.
	serverInstallCmd.Flags().StringVar(&serverFlagDataDir, "data-dir", "", "override data directory (default: /var/lib/doomsday)")

	// Client add flags.
	serverClientAddCmd.Flags().StringVar(&serverFlagQuota, "quota", "", "storage quota (e.g. 100GiB, 500GB)")
	serverClientAddCmd.Flags().BoolVar(&serverFlagAppendOnly, "append-only", false, "restrict client to append-only operations (no delete/overwrite)")

	// Wire up subcommands.
	serverClientCmd.AddCommand(serverClientAddCmd)
	serverClientCmd.AddCommand(serverClientRemoveCmd)
	serverClientCmd.AddCommand(serverClientListCmd)

	serverCmd.AddCommand(serverInitCmd)
	serverCmd.AddCommand(serverEditCmd)
	serverCmd.AddCommand(serverServeCmd)
	serverCmd.AddCommand(serverInstallCmd)
	serverCmd.AddCommand(serverUninstallCmd)
	serverCmd.AddCommand(serverStatusCmd)
	serverCmd.AddCommand(serverClientCmd)

}

// ---------------------------------------------------------------------------
// server init
// ---------------------------------------------------------------------------

func runServerInit(cmd *cobra.Command, args []string) error {
	cfgPath := serverConfigPath()

	if _, err := os.Stat(cfgPath); err == nil {
		return fmt.Errorf("server config already exists at %s", cfgPath)
	}

	// Pick a sensible default data_dir: /var/lib/doomsday if writable, else ~/doomsday-data.
	dataDir := serverFlagDataDir
	if dataDir == "" {
		dataDir = detectServerDataDir()
	}

	// Generate host key.
	hostKeyPEM, err := generateHostKeyPEM()
	if err != nil {
		return err
	}

	// Ensure config directory exists.
	cfgDir := filepath.Dir(cfgPath)
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	cfg := &config.ServerConfig{
		DataDir: dataDir,
		Host:    "0.0.0.0",
		Port:    8420,
		HostKey: hostKeyPEM,
	}

	if err := config.SaveServer(cfgPath, cfg); err != nil {
		return err
	}

	if flagJSON {
		type initResult struct {
			ConfigPath string `json:"config_path"`
			DataDir    string `json:"data_dir"`
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(initResult{ConfigPath: cfgPath, DataDir: dataDir})
	}

	logger.Info("Server config created", "path", cfgPath)
	logger.Info("Data directory", "path", dataDir)
	logger.Info("Start the server with: doomsday server serve")
	return nil
}


// detectServerDataDir returns a sensible default for data_dir.
// Prefers /var/lib/doomsday if it exists or can be created; falls back to ~/doomsday-data.
func detectServerDataDir() string {
	sysDir := "/var/lib/doomsday"
	// Check if it already exists.
	if fi, err := os.Stat(sysDir); err == nil && fi.IsDir() {
		return sysDir
	}
	// Check if we can create it (i.e. running as root).
	if err := os.MkdirAll(sysDir, 0700); err == nil {
		return sysDir
	}
	// Fall back to home directory.
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "doomsday-data")
}

// ---------------------------------------------------------------------------
// server serve
// ---------------------------------------------------------------------------

func runServerServe(cmd *cobra.Command, args []string) error {
	cfg, err := loadServerConfig()
	if err != nil {
		return err
	}

	dataDir := config.ExpandPath(cfg.DataDir)
	if dataDir == "" {
		return fmt.Errorf("data_dir is required in server config")
	}

	// Auto-generate host key if missing from config.
	if cfg.HostKey == "" {
		logger.Info("Generating SSH host key")
		hostKeyPEM, genErr := generateHostKeyPEM()
		if genErr != nil {
			return fmt.Errorf("generate host key: %w", genErr)
		}
		cfg.HostKey = hostKeyPEM
		cfgPath := serverConfigPath()
		if saveErr := config.SaveServer(cfgPath, cfg); saveErr != nil {
			return fmt.Errorf("save config with host key: %w", saveErr)
		}
		logger.Info("Host key saved to config")
	}

	// Apply CLI overrides.
	host := cfg.Host
	if serverFlagHost != "" {
		host = serverFlagHost
	}
	port := cfg.Port
	if serverFlagPort != 0 {
		port = serverFlagPort
	}

	// Parse client configs from server.yaml into server.ClientConfig.
	serverClients, err := parseServerClients(cfg)
	if err != nil {
		return err
	}

	listenAddr := fmt.Sprintf("%s:%d", host, port)

	// Build a reload function that re-reads server.yaml when it changes.
	cfgPath := serverConfigPath()
	var lastModTime time.Time
	if fi, err := os.Stat(cfgPath); err == nil {
		lastModTime = fi.ModTime()
	}
	reloadClients := func() ([]server.ClientConfig, error) {
		fi, err := os.Stat(cfgPath)
		if err != nil {
			return nil, fmt.Errorf("stat config: %w", err)
		}
		if !fi.ModTime().After(lastModTime) {
			return nil, nil // no change
		}
		lastModTime = fi.ModTime()

		newCfg, err := config.LoadServer(cfgPath)
		if err != nil {
			return nil, fmt.Errorf("reload config: %w", err)
		}
		return parseServerClients(newCfg)
	}

	srvCfg := server.Config{
		ListenAddr:    listenAddr,
		HostKeyPEM:    []byte(cfg.HostKey),
		DataDir:       dataDir,
		Clients:       serverClients,
		ReloadClients: reloadClients,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// If tailscale_hostname is set, create a tsnet server and provide a Tailscale listener.
	var tsServer *tsnet.Server
	if cfg.TailscaleEnabled() {
		cfgDir := filepath.Dir(serverConfigPath())
		tsStateDir := filepath.Join(cfgDir, "tsnet")
		if mkErr := os.MkdirAll(tsStateDir, 0700); mkErr != nil {
			return fmt.Errorf("create tsnet state dir: %w", mkErr)
		}
		// tsnet expects the short hostname (e.g. "doomsday"), not the FQDN.
		// Config stores the full FQDN (e.g. "doomsday.tail1234.ts.net").
		tsHostShort := cfg.TailscaleHostname
		if i := strings.IndexByte(tsHostShort, '.'); i > 0 {
			tsHostShort = tsHostShort[:i]
		}
		tsServer = &tsnet.Server{
			Hostname: tsHostShort,
			Dir:      tsStateDir,
		}
		if cfg.TailscaleAuthKey != "" {
			tsServer.AuthKey = cfg.TailscaleAuthKey
		}
		ln, tsErr := tsServer.Listen("tcp", fmt.Sprintf(":%d", port))
		if tsErr != nil {
			return fmt.Errorf("tailscale listen: %w", tsErr)
		}
		srvCfg.Listener = ln
		listenAddr = fmt.Sprintf("%s:%d (tailscale)", cfg.TailscaleHostname, port)
		logger.Info("Tailscale node ready", "hostname", cfg.TailscaleHostname)
		defer tsServer.Close()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			logger.Warn("Received signal, shutting down...", "signal", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	logger.Info("Starting SFTP server", "addr", listenAddr, "data_dir", dataDir, "clients", len(serverClients))

	if flagJSON {
		type serverServeJSON struct {
			Action    string `json:"action"`
			Addr      string `json:"addr"`
			DataDir   string `json:"data_dir"`
			Tailscale bool   `json:"tailscale"`
			Clients   int    `json:"clients"`
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(serverServeJSON{
			Action:    "serve",
			Addr:      listenAddr,
			DataDir:   dataDir,
			Tailscale: cfg.TailscaleEnabled(),
			Clients:   len(serverClients),
		})
	}

	err = server.Start(ctx, srvCfg)
	if err != nil && err != context.Canceled {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}

// parseServerClients converts config client entries to server.ClientConfig.
func parseServerClients(cfg *config.ServerConfig) ([]server.ClientConfig, error) {
	var clients []server.ClientConfig
	for _, cl := range cfg.Clients {
		pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(cl.PublicKey))
		if err != nil {
			return nil, fmt.Errorf("parse public key for client %q: %w", cl.Name, err)
		}

		var quotaBytes int64
		if cl.Quota != "" {
			quotaBytes, err = parseQuota(cl.Quota)
			if err != nil {
				return nil, fmt.Errorf("parse quota for client %q: %w", cl.Name, err)
			}
		}

		clients = append(clients, server.ClientConfig{
			Name:       cl.Name,
			PublicKey:  pubKey,
			QuotaBytes: quotaBytes,
			AppendOnly: cl.AppendOnly,
		})
	}
	return clients, nil
}

// ---------------------------------------------------------------------------
// server client add
// ---------------------------------------------------------------------------

func runServerClientAdd(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfgPath := serverConfigPath()
	cfg, err := loadServerConfig()
	if err != nil {
		return err
	}

	// Generate Ed25519 keypair.
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate keypair: %w", err)
	}

	// Encode private key seed as base64url (43 chars, no padding).
	seed := privKey.Seed()
	seedB64 := base64.RawURLEncoding.EncodeToString(seed)

	// Encode public key in SSH authorized_keys format.
	sshPubKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return fmt.Errorf("convert public key: %w", err)
	}
	pubKeyStr := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPubKey)))

	// Add client to config.
	clientCfg := config.ServerClientConfig{
		Name:       name,
		PublicKey:  pubKeyStr,
		Quota:      serverFlagQuota,
		AppendOnly: serverFlagAppendOnly,
	}
	if err := cfg.AddClient(clientCfg); err != nil {
		return err
	}

	// Save config.
	if err := config.SaveServer(cfgPath, cfg); err != nil {
		return err
	}

	// Generate host key if missing from config.
	if cfg.HostKey == "" {
		logger.Info("Generating SSH host key")
		hostKeyPEM, genErr := generateHostKeyPEM()
		if genErr != nil {
			logger.Warn("Could not generate host key", "error", genErr)
		} else {
			cfg.HostKey = hostKeyPEM
			if saveErr := config.SaveServer(cfgPath, cfg); saveErr != nil {
				logger.Warn("Could not save host key to config", "error", saveErr)
			}
		}
	}
	hostKeyFP := hostKeyFingerprint(cfg.HostKey)

	// Determine server address for the one-liner.
	// Prefer Tailscale hostname if configured.
	serverHost := cfg.TailscaleHostname
	if serverHost == "" {
		serverHost = cfg.Host
		if serverHost == "0.0.0.0" || serverHost == "" {
			serverHost, _ = os.Hostname()
		}
	}

	if flagJSON {
		type clientAddJSON struct {
			Name        string `json:"name"`
			Fingerprint string `json:"fingerprint"`
			SSHKey      string `json:"ssh_key"`
			PublicKey   string `json:"public_key"`
			HostKey     string `json:"host_key,omitempty"`
			ServerHost  string `json:"server_host"`
			ServerPort  int    `json:"server_port"`
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(clientAddJSON{
			Name:        name,
			Fingerprint: ssh.FingerprintSHA256(sshPubKey),
			SSHKey:      seedB64,
			PublicKey:   pubKeyStr,
			HostKey:     hostKeyFP,
			ServerHost:  serverHost,
			ServerPort:  cfg.Port,
		})
	}

	fmt.Printf("Client %q created.\n\n", name)
	fmt.Printf("Add to client config (client.yaml) under destinations:\n\n")
	fmt.Printf("  - name: server\n")
	fmt.Printf("    type: sftp\n")
	fmt.Printf("    host: %s\n", serverHost)
	fmt.Printf("    port: %d\n", cfg.Port)
	fmt.Printf("    user: %s\n", name)
	fmt.Printf("    ssh_key: %q\n", seedB64)
	if hostKeyFP != "" {
		fmt.Printf("    host_key: %q\n", hostKeyFP)
	}

	fmt.Println()
	logger.Info("The running server will pick up this change automatically")

	return nil
}

// hostKeyFingerprint returns the SHA256 fingerprint of a PEM-encoded host key.
func hostKeyFingerprint(pemData string) string {
	if pemData == "" {
		return ""
	}
	signer, err := ssh.ParsePrivateKey([]byte(pemData))
	if err != nil {
		return ""
	}
	return ssh.FingerprintSHA256(signer.PublicKey())
}

// ---------------------------------------------------------------------------
// server client remove
// ---------------------------------------------------------------------------

func runServerClientRemove(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfgPath := serverConfigPath()
	cfg, err := loadServerConfig()
	if err != nil {
		return err
	}

	if err := cfg.RemoveClient(name); err != nil {
		return err
	}

	if err := config.SaveServer(cfgPath, cfg); err != nil {
		return err
	}

	if flagJSON {
		type removeResult struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(removeResult{Name: name, Message: "Client removed (data preserved)"})
	}

	logger.Info("Client removed (data preserved)", "name", name)
	logger.Info("Restart the server for changes to take effect")
	return nil
}

// ---------------------------------------------------------------------------
// server client list
// ---------------------------------------------------------------------------

func runServerClientList(cmd *cobra.Command, args []string) error {
	cfg, err := loadServerConfig()
	if err != nil {
		return err
	}

	type clientInfo struct {
		Name        string `json:"name"`
		Fingerprint string `json:"fingerprint,omitempty"`
		Quota       string `json:"quota,omitempty"`
		AppendOnly  bool   `json:"append_only,omitempty"`
	}

	var clients []clientInfo
	for _, cl := range cfg.Clients {
		ci := clientInfo{
			Name:       cl.Name,
			Quota:      cl.Quota,
			AppendOnly: cl.AppendOnly,
		}
		if pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(cl.PublicKey)); err == nil {
			ci.Fingerprint = ssh.FingerprintSHA256(pubKey)
		}
		clients = append(clients, ci)
	}

	if flagJSON {
		type listResult struct {
			Clients []clientInfo `json:"clients"`
			Count   int          `json:"count"`
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(listResult{Clients: clients, Count: len(clients)})
	}

	if len(clients) == 0 {
		logger.Info("No clients registered")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tFINGERPRINT\tQUOTA\tMODE")
	fmt.Fprintln(w, "----\t-----------\t-----\t----")
	for _, c := range clients {
		quota := "unlimited"
		if c.Quota != "" {
			quota = c.Quota
		}
		mode := "read-write"
		if c.AppendOnly {
			mode = "append-only"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.Name, c.Fingerprint, quota, mode)
	}
	w.Flush()

	return nil
}

// ---------------------------------------------------------------------------
// server status
// ---------------------------------------------------------------------------

func runServerStatus(cmd *cobra.Command, args []string) error {
	cfgPath := serverConfigPath()

	// Detect uninitialized state.
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		return renderUninitializedServer(cfgPath)
	}

	cfg, cfgErr := loadServerConfig()

	// Detect daemon installation.
	installed, mechanism := detectDaemonInstalled()

	// Probe whether the server is actually responding.
	var responding bool
	if cfgErr == nil {
		responding = probeServer(cfg)
	}

	if flagJSON {
		return renderServerStatusJSON(cfg, cfgErr, cfgPath, responding, installed, mechanism)
	}

	return renderServerDashboard(cfg, cfgErr, cfgPath, responding, installed, mechanism)
}

func renderUninitializedServer(cfgPath string) error {
	if flagJSON {
		out := map[string]string{
			"status":  "uninitialized",
			"message": "No server configuration found",
			"config":  cfgPath,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	fmt.Println()
	fmt.Print(cliStyles.Brand.Render(banner))
	fmt.Println()
	fmt.Println(cliStyles.Warning.Render("  No server configuration found."))
	fmt.Println()
	fmt.Println(kv("Expected:", cfgPath))
	fmt.Println()
	fmt.Println("  Get started:")
	fmt.Println(cliStyles.Success.Render("    doomsday server init"))
	fmt.Println()
	fmt.Println(cliStyles.Muted.Render("  This will create a server config with an SSH host key."))
	fmt.Println(cliStyles.Muted.Render("  Then add clients with: doomsday server client add <name>"))
	fmt.Println()

	return nil
}

func renderServerStatusJSON(cfg *config.ServerConfig, cfgErr error, cfgPath string, responding, installed bool, mechanism string) error {
	type clientInfo struct {
		Name       string `json:"name"`
		Quota      string `json:"quota,omitempty"`
		AppendOnly bool   `json:"append_only,omitempty"`
		DataSize   int64  `json:"data_size_bytes,omitempty"`
	}

	type statusJSON struct {
		Responding  bool         `json:"responding"`
		Installed   bool         `json:"installed"`
		Mechanism   string       `json:"mechanism"`
		ConfigPath  string       `json:"config_path"`
		DataDir     string       `json:"data_dir,omitempty"`
		ListenAddr  string       `json:"listen_addr,omitempty"`
		Tailscale   string       `json:"tailscale,omitempty"`
		ClientCount int          `json:"client_count"`
		Clients     []clientInfo `json:"clients,omitempty"`
		ConfigError string       `json:"config_error,omitempty"`
	}

	out := statusJSON{
		Responding: responding,
		Installed:  installed,
		Mechanism:  mechanism,
		ConfigPath: cfgPath,
	}

	if cfgErr != nil {
		out.ConfigError = cfgErr.Error()
	} else {
		out.DataDir = config.ExpandPath(cfg.DataDir)
		out.ListenAddr = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
		if cfg.TailscaleEnabled() {
			out.Tailscale = cfg.TailscaleHostname
		}
		out.ClientCount = len(cfg.Clients)

		for _, cl := range cfg.Clients {
			ci := clientInfo{
				Name:       cl.Name,
				Quota:      cl.Quota,
				AppendOnly: cl.AppendOnly,
			}
			clientDir := filepath.Join(config.ExpandPath(cfg.DataDir), cl.Name)
			ci.DataSize = dirSize(clientDir)
			out.Clients = append(out.Clients, ci)
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func renderServerDashboard(cfg *config.ServerConfig, cfgErr error, cfgPath string, responding, installed bool, mechanism string) error {
	fmt.Println()
	fmt.Print(cliStyles.Brand.Render(banner))
	fmt.Printf("  %s\n", cliStyles.Muted.Render("SFTP Backup Server"))

	if cfgErr != nil {
		fmt.Println()
		fmt.Println("  " + cliStyles.Error.Render("Failed to load config: "+cfgErr.Error()))
		return nil
	}

	// ── Server ──
	fmt.Println()
	fmt.Println(sectionHeader("Server"))
	fmt.Println(kv("Config:", cfgPath))

	dataDir := config.ExpandPath(cfg.DataDir)
	dataDirExists := pathExists(dataDir)
	fmt.Println(kv("Data directory:", dataDir))
	if !dataDirExists {
		fmt.Println("  " + cliStyles.Warning.Render("  (directory does not exist)"))
	}

	if cfg.TailscaleEnabled() {
		fmt.Println(kv("Listen:", fmt.Sprintf("%s:%d (tailscale)", cfg.TailscaleHostname, cfg.Port)))
	} else {
		fmt.Println(kv("Listen:", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)))
	}

	hostFP := hostKeyFingerprint(cfg.HostKey)
	if hostFP != "" {
		fmt.Println(kv("Host key:", hostFP))
	} else {
		fmt.Println(kv("Host key:", cliStyles.Warning.Render("not generated")))
	}

	// ── Status ──
	fmt.Println()
	fmt.Println(sectionHeader("Status"))
	if responding {
		fmt.Println(kv("Server:", statusLabel(true, "responding", "")))
	} else {
		fmt.Println(kv("Server:", statusLabel(false, "", "not responding")))
	}
	if installed {
		fmt.Println(kv("Daemon:", statusLabel(true, "installed", "")+" "+cliStyles.Muted.Render("("+mechanism+")")))
	} else {
		hint := ""
		if mechanism != "" {
			hint = " " + cliStyles.Muted.Render("("+mechanism+")")
		}
		fmt.Println(kv("Daemon:", cliStyles.Warning.Render("not installed")+hint+" "+cliStyles.Muted.Render("(doomsday server install)")))
	}

	// ── Clients ──
	fmt.Println()
	fmt.Println(sectionHeader("Clients"))

	if len(cfg.Clients) == 0 {
		fmt.Println("  " + cliStyles.Muted.Render("No clients registered"))
		fmt.Println("  " + cliStyles.Muted.Render("Add one with: doomsday server client add <name>"))
	} else {
		var totalSize int64
		for _, cl := range cfg.Clients {
			clientDir := filepath.Join(dataDir, cl.Name)
			size := dirSize(clientDir)
			totalSize += size

			dot := statusDot(pathExists(clientDir))
			mode := cliStyles.Muted.Render("read-write")
			if cl.AppendOnly {
				mode = cliStyles.Warning.Render("append-only")
			}

			quota := cliStyles.Muted.Render("unlimited")
			if cl.Quota != "" {
				quota = cl.Quota
			}

			fmt.Printf("  %s %s  %s  quota: %s  data: %s\n",
				dot,
				cliStyles.Value.Render(cl.Name),
				mode,
				quota,
				formatBytes(size),
			)

			// Show fingerprint.
			if pubKey, _, _, _, pErr := ssh.ParseAuthorizedKey([]byte(cl.PublicKey)); pErr == nil {
				fp := ssh.FingerprintSHA256(pubKey)
				fmt.Printf("      %s\n", cliStyles.Muted.Render(fp))
			}
		}

		fmt.Println()
		fmt.Println(kv("Total clients:", fmt.Sprintf("%d", len(cfg.Clients))))
		fmt.Println(kv("Total data:", formatBytes(totalSize)))
	}

	fmt.Println()
	return nil
}

// probeServer tries a TCP connection to the server's configured listen address
// to determine if it is actually running and accepting connections.
// In Tailscale mode, it probes the tailnet hostname instead of localhost.
func probeServer(cfg *config.ServerConfig) bool {
	host := "127.0.0.1"
	if cfg.TailscaleEnabled() {
		host = cfg.TailscaleHostname
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", cfg.Port))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// detectDaemonInstalled checks if the server is installed as a system daemon.
func detectDaemonInstalled() (bool, string) {
	switch runtime.GOOS {
	case "linux":
		if _, err := os.Stat(systemdUnitPath); err == nil {
			return true, "systemd"
		}
		return false, "systemd"
	case "darwin":
		if _, err := os.Stat(launchdPlistPath); err == nil {
			return true, "launchd"
		}
		return false, "launchd"
	default:
		return false, ""
	}
}

// dirSize walks a directory and returns total file size in bytes.
func dirSize(path string) int64 {
	var size int64
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0
	}
	for _, entry := range entries {
		if entry.IsDir() {
			size += dirSize(filepath.Join(path, entry.Name()))
		} else {
			info, err := entry.Info()
			if err == nil {
				size += info.Size()
			}
		}
	}
	return size
}

// ---------------------------------------------------------------------------
// server install / uninstall
// ---------------------------------------------------------------------------

const systemdUnitTemplate = `[Unit]
Description=Doomsday SFTP Backup Server
After=network.target

[Service]
Type=simple
User=doomsday
Group=doomsday
ExecStart={{.BinaryPath}} server serve --config {{.ConfigPath}}
Restart=on-failure
RestartSec=5

# Hardening
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
NoNewPrivileges=yes
ReadWritePaths={{.DataDir}}

[Install]
WantedBy=multi-user.target
`

const launchdPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.doomsday.server</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath}}</string>
        <string>server</string>
        <string>serve</string>
        <string>--config</string>
        <string>{{.ConfigPath}}</string>
    </array>
    <key>UserName</key>
    <string>_doomsday</string>
    <key>KeepAlive</key>
    <true/>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardErrorPath</key>
    <string>/var/log/doomsday-server.log</string>
    <key>StandardOutPath</key>
    <string>/var/log/doomsday-server.log</string>
</dict>
</plist>
`

type serviceTemplateData struct {
	BinaryPath string
	ConfigPath string
	DataDir    string
}

const (
	systemdUnitPath      = "/etc/systemd/system/doomsday-server.service"
	launchdPlistPath     = "/Library/LaunchDaemons/com.doomsday.server.plist"
	defaultServerDataDir = "/var/lib/doomsday"
)

func runServerInstall(cmd *cobra.Command, args []string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("server install requires root privileges (try: sudo doomsday server install)")
	}

	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("determine binary path: %w", err)
	}
	binaryPath, err = filepath.EvalSymlinks(binaryPath)
	if err != nil {
		return fmt.Errorf("resolve binary path: %w", err)
	}

	// Determine config path — create a system-level config if one doesn't exist.
	cfgPath := serverConfigPath()
	dataDir := defaultServerDataDir
	if serverFlagDataDir != "" {
		dataDir = serverFlagDataDir
	}

	// Create server config if it doesn't exist.
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		hostKeyPEM, genErr := generateHostKeyPEM()
		if genErr != nil {
			return fmt.Errorf("generate host key: %w", genErr)
		}
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
			return fmt.Errorf("create config dir: %w", err)
		}
		installCfg := &config.ServerConfig{
			DataDir: dataDir,
			Host:    "0.0.0.0",
			Port:    8420,
			HostKey: hostKeyPEM,
		}
		if err := config.SaveServer(cfgPath, installCfg); err != nil {
			return err
		}
	} else {
		// Load existing config to get data dir.
		if cfg, loadErr := config.LoadServer(cfgPath); loadErr == nil {
			dataDir = config.ExpandPath(cfg.DataDir)
		}
	}

	data := serviceTemplateData{
		BinaryPath: binaryPath,
		ConfigPath: cfgPath,
		DataDir:    dataDir,
	}

	switch runtime.GOOS {
	case "linux":
		return installServerLinux(data)
	case "darwin":
		return installServerDarwin(data)
	default:
		return fmt.Errorf("server install is only supported on Linux (systemd) and macOS (launchd)")
	}
}

func installServerLinux(data serviceTemplateData) error {
	if err := ensureUserLinux("doomsday"); err != nil {
		return fmt.Errorf("create service user: %w", err)
	}

	if err := os.MkdirAll(data.DataDir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	if err := runCommand("chown", "-R", "doomsday:doomsday", data.DataDir); err != nil {
		return fmt.Errorf("chown data directory: %w", err)
	}

	tmpl, err := template.New("systemd").Parse(systemdUnitTemplate)
	if err != nil {
		return fmt.Errorf("parse systemd template: %w", err)
	}

	f, err := os.Create(systemdUnitPath)
	if err != nil {
		return fmt.Errorf("create unit file: %w", err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}
	f.Close()

	if err := runCommand("systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if err := runCommand("systemctl", "enable", "doomsday-server"); err != nil {
		return fmt.Errorf("systemctl enable: %w", err)
	}
	if err := runCommand("systemctl", "start", "doomsday-server"); err != nil {
		return fmt.Errorf("systemctl start: %w", err)
	}

	msg := fmt.Sprintf("Installed systemd service at %s (config: %s)", systemdUnitPath, data.ConfigPath)

	if flagJSON {
		type result struct {
			Installed bool   `json:"installed"`
			Platform  string `json:"platform"`
			UnitPath  string `json:"unit_path"`
			Message   string `json:"message"`
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result{Installed: true, Platform: "linux", UnitPath: systemdUnitPath, Message: msg})
	}

	logger.Info(msg)
	return nil
}

func installServerDarwin(data serviceTemplateData) error {
	if err := ensureUserDarwin("_doomsday"); err != nil {
		return fmt.Errorf("create service user: %w", err)
	}

	if err := os.MkdirAll(data.DataDir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	if err := runCommand("chown", "-R", "_doomsday:staff", data.DataDir); err != nil {
		return fmt.Errorf("chown data directory: %w", err)
	}

	tmpl, err := template.New("launchd").Parse(launchdPlistTemplate)
	if err != nil {
		return fmt.Errorf("parse launchd template: %w", err)
	}

	f, err := os.Create(launchdPlistPath)
	if err != nil {
		return fmt.Errorf("create plist file: %w", err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("write plist file: %w", err)
	}
	f.Close()

	if err := runCommand("launchctl", "load", "-w", launchdPlistPath); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}

	msg := fmt.Sprintf("Installed launchd daemon at %s (config: %s)", launchdPlistPath, data.ConfigPath)

	if flagJSON {
		type result struct {
			Installed bool   `json:"installed"`
			Platform  string `json:"platform"`
			Plist     string `json:"plist"`
			Message   string `json:"message"`
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result{Installed: true, Platform: "darwin", Plist: launchdPlistPath, Message: msg})
	}

	logger.Info(msg)
	return nil
}

func ensureUserLinux(username string) error {
	if err := runCommand("id", username); err == nil {
		return nil
	}
	return runCommand("useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", username)
}

func ensureUserDarwin(username string) error {
	if err := runCommand("dscl", ".", "-read", "/Users/"+username); err == nil {
		return nil
	}
	for uid := 400; uid < 500; uid++ {
		uidStr := fmt.Sprintf("%d", uid)
		if runCommand("dscl", ".", "-read", "/Users/"+uidStr) != nil {
			cmds := [][]string{
				{"dscl", ".", "-create", "/Users/" + username},
				{"dscl", ".", "-create", "/Users/" + username, "UniqueID", uidStr},
				{"dscl", ".", "-create", "/Users/" + username, "PrimaryGroupID", "20"},
				{"dscl", ".", "-create", "/Users/" + username, "UserShell", "/usr/bin/false"},
				{"dscl", ".", "-create", "/Users/" + username, "RealName", "Doomsday Server"},
				{"dscl", ".", "-create", "/Users/" + username, "NFSHomeDirectory", "/var/empty"},
			}
			for _, c := range cmds {
				if err := runCommand(c[0], c[1:]...); err != nil {
					return fmt.Errorf("dscl command %v: %w", c, err)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("could not find a free UID in range 400-499")
}

func runServerUninstall(cmd *cobra.Command, args []string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("server uninstall requires root privileges (try: sudo doomsday server uninstall)")
	}

	switch runtime.GOOS {
	case "linux":
		return uninstallServerLinux()
	case "darwin":
		return uninstallServerDarwin()
	default:
		return fmt.Errorf("server uninstall is only supported on Linux and macOS")
	}
}

func uninstallServerLinux() error {
	_ = runCommand("systemctl", "stop", "doomsday-server")
	_ = runCommand("systemctl", "disable", "doomsday-server")

	removed := false
	if _, err := os.Stat(systemdUnitPath); err == nil {
		if err := os.Remove(systemdUnitPath); err != nil {
			return fmt.Errorf("remove unit file: %w", err)
		}
		removed = true
	}

	_ = runCommand("systemctl", "daemon-reload")

	msg := fmt.Sprintf("Removed systemd service %s", systemdUnitPath)
	if !removed {
		msg = "No systemd service found to remove"
	}
	msg += "\nData directory was preserved"

	if flagJSON {
		type result struct {
			Uninstalled   bool   `json:"uninstalled"`
			Platform      string `json:"platform"`
			DataPreserved bool   `json:"data_preserved"`
			Message       string `json:"message"`
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result{Uninstalled: removed, Platform: "linux", DataPreserved: true, Message: msg})
	}

	logger.Info(msg)
	return nil
}

func uninstallServerDarwin() error {
	_ = runCommand("launchctl", "unload", "-w", launchdPlistPath)

	removed := false
	if _, err := os.Stat(launchdPlistPath); err == nil {
		if err := os.Remove(launchdPlistPath); err != nil {
			return fmt.Errorf("remove plist file: %w", err)
		}
		removed = true
	}

	msg := fmt.Sprintf("Removed launchd daemon %s", launchdPlistPath)
	if !removed {
		msg = "No launchd daemon found to remove"
	}
	msg += "\nData directory was preserved"

	if flagJSON {
		type result struct {
			Uninstalled   bool   `json:"uninstalled"`
			Platform      string `json:"platform"`
			DataPreserved bool   `json:"data_preserved"`
			Message       string `json:"message"`
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result{Uninstalled: removed, Platform: "darwin", DataPreserved: true, Message: msg})
	}

	logger.Info(msg)
	return nil
}

// ---------------------------------------------------------------------------
// host key generation
// ---------------------------------------------------------------------------

// generateHostKeyPEM generates an Ed25519 SSH host key and returns it as PEM.
func generateHostKeyPEM() (string, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate host key: %w", err)
	}
	pemBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return "", fmt.Errorf("marshal host key: %w", err)
	}
	return string(pem.EncodeToMemory(pemBlock)), nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// serverConfigPath returns the server config file path.
func serverConfigPath() string {
	if serverFlagConfig != "" {
		return serverFlagConfig
	}
	return config.DefaultServerConfigPath()
}

// loadServerConfig loads the server config from the resolved path.
func loadServerConfig() (*config.ServerConfig, error) {
	cfgPath := serverConfigPath()
	cfg, err := config.LoadServer(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load server config %s: %w (run 'doomsday server init' first)", cfgPath, err)
	}
	return cfg, nil
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func parseQuota(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}

	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("no numeric value found")
	}

	numStr := s[:i]
	unit := strings.TrimSpace(s[i:])

	var num float64
	if _, err := fmt.Sscanf(numStr, "%f", &num); err != nil {
		return 0, fmt.Errorf("invalid number %q: %w", numStr, err)
	}

	var multiplier float64
	switch strings.ToLower(unit) {
	case "", "b":
		multiplier = 1
	case "kb":
		multiplier = 1e3
	case "mb":
		multiplier = 1e6
	case "gb":
		multiplier = 1e9
	case "tb":
		multiplier = 1e12
	case "kib":
		multiplier = 1024
	case "mib":
		multiplier = 1024 * 1024
	case "gib":
		multiplier = 1024 * 1024 * 1024
	case "tib":
		multiplier = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("unknown unit %q", unit)
	}

	return int64(num * multiplier), nil
}

