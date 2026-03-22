# Doomsday Development Guide

## Quick Reference
- **Module:** `github.com/jclement/doomsday`
- **Go version:** 1.25+ with `CGO_ENABLED=0`
- **Tooling:** Uses `mise` for tool management. All Go commands run via `mise exec --`.
- **Spec:** See `spec.md` for all design decisions — this is the source of truth

## Commands
All commands use `mise exec --` to ensure correct Go version:
- Build: `mise exec -- go build -o doomsday ./cmd/doomsday`
- Test: `mise exec -- go test -race -count=1 ./...`
- Test single package: `mise exec -- go test -race -v ./internal/crypto/`
- Lint: `mise exec -- go vet ./...`
- Tidy: `mise exec -- go mod tidy`
- Fuzz: `mise exec -- go test -fuzz=FuzzPackReader ./internal/pack/ -fuzztime=30s`

## Package Dependency Rules (STRICT)
- `internal/types/` → no internal imports (stdlib only)
- Leaf packages (`crypto/`, `chunker/`, `compress/`, `pack/`, `index/`) → import `types/` only
- Shared data packages (`tree/`, `snapshot/`) → import `types/` only. May be imported directly by orchestration-level packages for type construction
- `internal/repo/` → orchestrates leaf packages. Leaf packages NEVER import repo
- `internal/backup/`, `internal/restore/`, `internal/prune/`, `internal/check/` → import `repo/` and leaf/shared packages. Never import each other
- `internal/backend/*` → import `types/` only
- `cmd/doomsday/` → imports everything, nothing imports it

## Conventions
- Table-driven tests with `t.Run()` subtests
- Errors: `fmt.Errorf("package.Function: %w", err)` — always wrap with context
- No Viper. Config via `gopkg.in/yaml.v3` directly
- No `interface{}` — use generics or concrete types
- All crypto: Go stdlib (`crypto/aes`, `crypto/cipher`, `crypto/hmac`, `crypto/rand`) + `golang.org/x/crypto` (hkdf, scrypt). Never roll custom primitives.
- Context as first param on anything that does I/O
- Structured logging: `charmbracelet/log` for CLI output, stdlib `log/slog` for internal

## Key Dependencies
| Package | Purpose |
|---------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/charmbracelet/bubbletea` | TUI framework |
| `github.com/charmbracelet/lipgloss` | TUI styling |
| `github.com/charmbracelet/log` | Colorful CLI logging |
| `github.com/charmbracelet/huh` | TUI forms |
| `gopkg.in/yaml.v3` | YAML config |
| `github.com/klauspost/compress/zstd` | Compression |
| `github.com/PlakarKorp/go-cdc-chunkers` | FastCDC chunking |
| `github.com/pkg/sftp` | SFTP client + server |
| `github.com/minio/minio-go/v7` | S3-compatible storage |
| `github.com/tyler-smith/go-bip39` | Recovery phrase generation |
| `golang.org/x/crypto` | HKDF, scrypt |
| `golang.org/x/sync` | errgroup for concurrent pipelines |
| `github.com/creativeprojects/go-selfupdate` | Self-update |

## Testing Principles
- Every package has `_test.go`. No exceptions.
- Use `-race` always
- Property-based tests for crypto, chunker, pack format
- Fuzz tests for pack reader, tree serializer, config parser, SFTP handler
- Integration tests: backup→restore roundtrip must be byte-identical
- `TestMain` for expensive setup (generating test keys once)
