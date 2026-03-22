// Package server implements the Doomsday SFTP server mode.
//
// The server uses golang.org/x/crypto/ssh for SSH transport and github.com/pkg/sftp
// RequestServer for SFTP protocol handling. Each authenticated client is jailed to its
// own data directory with quota enforcement and an append-only whitelist of allowed operations.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// maxConcurrentConnections limits how many SSH connections the server handles
// simultaneously, preventing resource exhaustion from connection floods.
const maxConcurrentConnections = 100

// Config holds the server configuration.
type Config struct {
	// ListenAddr is the address to listen on (default ":8420").
	ListenAddr string

	// HostKeyPEM is the SSH host private key in PEM format.
	HostKeyPEM []byte

	// DataDir is the root data directory. Each client gets a subdirectory.
	DataDir string

	// Clients is the set of authorized clients (from server.yaml).
	Clients []ClientConfig

	// Listener is an optional pre-created listener (e.g. from tsnet).
	// When set, ListenAddr is ignored and this listener is used directly.
	Listener net.Listener

	// ReloadClients, if non-nil, is called periodically to get an updated client
	// list. The server hot-swaps the client set without restarting. The function
	// should return (nil, nil) if the config hasn't changed.
	ReloadClients func() ([]ClientConfig, error)
}

// Server is the Doomsday SFTP server.
type Server struct {
	config   Config
	clients  *ClientManager
	listener net.Listener
	sshCfg   *ssh.ServerConfig
	wg       sync.WaitGroup
	logger   *log.Logger
}

// Start creates and runs the SFTP server. It blocks until ctx is cancelled or an
// unrecoverable error occurs. On context cancellation it stops accepting new connections
// and waits for existing connections to drain.
func Start(ctx context.Context, config Config) error {
	if config.ListenAddr == "" {
		config.ListenAddr = ":8420"
	}

	if config.DataDir == "" {
		return errors.New("server: DataDir is required")
	}

	if len(config.HostKeyPEM) == 0 {
		return errors.New("server: HostKeyPEM is required")
	}

	// Ensure data directory exists.
	if err := os.MkdirAll(config.DataDir, 0700); err != nil {
		return fmt.Errorf("server: create data dir: %w", err)
	}

	logger := log.Default().With("component", "server")

	// Build client manager from config.
	cm, err := NewClientManager(config.DataDir, config.Clients)
	if err != nil {
		return fmt.Errorf("server: init client manager: %w", err)
	}

	srv := &Server{
		config:  config,
		clients: cm,
		logger:  logger,
	}

	// Build SSH server config with public key authentication.
	sshCfg := &ssh.ServerConfig{
		PublicKeyCallback: srv.publicKeyCallback,
	}

	hostKey, err := ssh.ParsePrivateKey(config.HostKeyPEM)
	if err != nil {
		return fmt.Errorf("server: parse host key: %w", err)
	}
	sshCfg.AddHostKey(hostKey)

	srv.sshCfg = sshCfg

	// Listen — use provided listener (e.g. Tailscale) or create one.
	var ln net.Listener
	if config.Listener != nil {
		ln = config.Listener
	} else {
		var listenErr error
		ln, listenErr = net.Listen("tcp", config.ListenAddr)
		if listenErr != nil {
			return fmt.Errorf("server: listen: %w", listenErr)
		}
	}
	srv.listener = ln
	logger.Info("listening", "addr", ln.Addr().String())

	// Accept loop in background; cancel on context done.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	// Config reload watcher.
	if config.ReloadClients != nil {
		go srv.watchConfig(ctx)
	}

	connSem := make(chan struct{}, maxConcurrentConnections)

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Expected when listener is closed due to context cancellation.
			if ctx.Err() != nil {
				break
			}
			logger.Warn("accept error", "err", err)
			continue
		}

		// Limit concurrent connections to prevent resource exhaustion.
		select {
		case connSem <- struct{}{}:
		default:
			logger.Warn("connection limit reached, rejecting", "remote", conn.RemoteAddr())
			conn.Close()
			continue
		}

		srv.wg.Add(1)
		go func() {
			defer func() { <-connSem }()
			srv.handleConn(ctx, conn)
		}()
	}

	// Wait for in-flight connections to finish.
	srv.wg.Wait()
	logger.Info("server stopped")
	return ctx.Err()
}

// publicKeyCallback verifies that the connecting key belongs to a registered client.
// On success it stores the client name in the SSH permissions extensions so that
// handleConn can look it up.
func (s *Server) publicKeyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	client, ok := s.clients.FindByKey(key)
	if !ok {
		s.logger.Warn("rejected unknown key", "user", conn.User(), "remote", conn.RemoteAddr())
		return nil, fmt.Errorf("unknown public key for %q", conn.User())
	}

	return &ssh.Permissions{
		Extensions: map[string]string{
			"client-name": client.Name,
		},
	}, nil
}

// handleConn performs the SSH handshake, waits for the "sftp" subsystem channel,
// and serves SFTP requests through the jailed handler.
func (s *Server) handleConn(ctx context.Context, netConn net.Conn) {
	defer s.wg.Done()
	defer netConn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(netConn, s.sshCfg)
	if err != nil {
		s.logger.Warn("ssh handshake failed", "remote", netConn.RemoteAddr(), "err", err)
		return
	}
	defer sshConn.Close()

	clientName := sshConn.Permissions.Extensions["client-name"]
	s.logger.Info("client connected", "client", clientName, "remote", sshConn.RemoteAddr())

	// Discard global requests (keepalive, etc.).
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}

		ch, requests, err := newCh.Accept()
		if err != nil {
			s.logger.Warn("channel accept failed", "client", clientName, "err", err)
			continue
		}

		// Handle subsystem requests on this channel.
		go s.handleSession(ctx, clientName, ch, requests)
	}

	s.logger.Info("client disconnected", "client", clientName)
}

// handleSession waits for an "sftp" subsystem request and serves it.
func (s *Server) handleSession(ctx context.Context, clientName string, ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()

	for req := range reqs {
		if req.Type != "subsystem" {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			continue
		}

		// Subsystem name is a uint32 length-prefixed string in the payload.
		if len(req.Payload) < 4 {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			continue
		}

		subsysLen := int(req.Payload[0])<<24 | int(req.Payload[1])<<16 | int(req.Payload[2])<<8 | int(req.Payload[3])
		if subsysLen+4 > len(req.Payload) {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			continue
		}
		subsystem := string(req.Payload[4 : 4+subsysLen])

		if subsystem != "sftp" {
			s.logger.Warn("rejected non-sftp subsystem", "subsystem", subsystem, "client", clientName)
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			continue
		}

		if req.WantReply {
			_ = req.Reply(true, nil)
		}

		s.serveSFTP(ctx, clientName, ch)
		return
	}
}

// serveSFTP creates the jailed SFTP handler for the client and serves requests.
func (s *Server) serveSFTP(ctx context.Context, clientName string, ch ssh.Channel) {
	client, ok := s.clients.Get(clientName)
	if !ok {
		s.logger.Error("client vanished during session", "client", clientName)
		return
	}

	// Resolve the jail directory (absolute, no symlinks).
	jailDir := filepath.Join(s.config.DataDir, clientName)
	if err := os.MkdirAll(jailDir, 0700); err != nil {
		s.logger.Error("create jail dir", "client", clientName, "err", err)
		return
	}

	realJail, err := filepath.EvalSymlinks(jailDir)
	if err != nil {
		s.logger.Error("resolve jail dir", "client", clientName, "err", err)
		return
	}

	handler := NewHandler(realJail, client.QuotaBytes, client.AppendOnly, s.logger.With("client", clientName))

	svr := sftp.NewRequestServer(ch, sftp.Handlers{
		FileGet:  handler,
		FilePut:  handler,
		FileCmd:  handler,
		FileList: handler,
	})

	s.logger.Info("sftp session started", "client", clientName, "jail", realJail)

	if err := svr.Serve(); err != nil {
		// io.EOF is normal on client disconnect.
		if !errors.Is(err, io.EOF) && err.Error() != "EOF" {
			s.logger.Warn("sftp session error", "client", clientName, "err", err)
		}
	}

	s.logger.Info("sftp session ended", "client", clientName)
}

// watchConfig periodically calls ReloadClients and hot-swaps the client set.
func (s *Server) watchConfig(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			clients, err := s.config.ReloadClients()
			if err != nil {
				s.logger.Warn("config reload failed", "err", err)
				continue
			}
			if clients == nil {
				continue // no change
			}
			if err := s.clients.Replace(clients); err != nil {
				s.logger.Warn("config reload: invalid client config", "err", err)
				continue
			}
			s.logger.Info("config reloaded", "clients", len(clients))
		}
	}
}
