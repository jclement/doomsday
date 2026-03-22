# Doomsday -- Backup for the End of the World

An all-in-one, end-to-end encrypted backup solution. Single Go binary. Beautiful TUI. Restic-level robustness. **Can never lose data.**

Repository: `github.com/jclement/doomsday`

---

## Principles

1. **Data integrity above all else.** Every code path that touches user data must be tested, verified, and paranoid. Corruption is not an option.
2. **End-to-end encryption by default.** The server never sees plaintext. Keys are generated client-side using standard, audited cryptography.
3. **Single binary, zero dependencies.** One `CGO_ENABLED=0` Go binary runs on Linux, macOS, and Windows. No runtime deps.
4. **Always incremental.** After the first full backup, only changed data moves over the wire. FastCDC content-defined chunking makes this sparse and efficient.
5. **Test everything.** Massive unit test coverage. Property-based tests for the chunker and crypto. Integration tests that simulate failures. Fuzz testing on pack file parsing.
6. **Whimsy matters.** Backup software doesn't have to be boring. Funny status messages, personality in the CLI output, delightful TUI interactions. Configurable: `whimsy = false` for the humorless. **Never in error output.**

---

## Phasing

### Phase 1 -- Core Engine + CLI + TUI (this spec)
- Backup engine (FastCDC chunking, encryption, compression, pack files, index)
- Repository format (local filesystem, SFTP, S3-compatible/Backblaze B2)
- One config file = one backup (use `-c alt.yaml` for separate backups)
- Client CLI under `doomsday client`: `backup`, `restore`, `snapshots`, `status`, `check`, `prune`, `forget`, `ls`, `find`, `cron`, `init`, `key`
- TUI: interactive interface for browsing, restoring, monitoring, configuration
- Headless mode with gorgeous colorful logs
- Cron mode with `doomsday client cron install` for systemd/launchd
- Notification system for cron failures
- Doomsday server mode (SFTP-based, quotas, append-only)
- Comprehensive test suite
- GitHub Actions CI/CD, GHCR images, multi-platform binaries, signed releases, self-update

### Phase 2 -- Web UI + NAT Traversal (future)
- Web UI: embedded SPA with Tailwind CSS, served from the binary via `embed.FS`
  - Configuration editor (destinations, backup configs, retention policies, keys)
  - On-demand backup/restore with live progress
  - Time Machine-style snapshot browser (timeline scrubber, file tree diffing)
  - Repository health dashboard, storage stats, cron history
  - Localhost-only by default, session-authenticated
- FUSE mount support (requires `hanwen/go-fuse`, pure Go -- cannot use `bazil.org/fuse` due to CGO)
- NAT traversal: UPnP/NAT-PMP auto-mapping, `doomsday server stdio` (rsync-style), built-in SSH reverse tunnel relay
- Prometheus metrics endpoint (`/metrics`)
- Multi-user / organization support

---

## Architecture Overview

```
doomsday
├── cmd/doomsday/              # Cobra CLI entrypoint
├── internal/
│   ├── types/                 # Shared types, interfaces (Backend, FileType, BlobID, etc.)
│   ├── backup/                # Backup orchestration (walk, snapshot, concurrent pipeline)
│   ├── restore/               # Restore orchestration (verify, atomic writes)
│   ├── repo/                  # Repository abstraction (open, read, write packs)
│   ├── backend/               # Storage backends
│   │   ├── local/             #   Local filesystem
│   │   ├── sftp/              #   SFTP (pkg/sftp client, connection pooling)
│   │   └── s3/                #   S3-compatible (B2, MinIO, Wasabi, R2, etc.)
│   ├── crypto/                # Encryption (AES-256-GCM, HKDF, key derivation)
│   ├── chunker/               # FastCDC content-defined chunking
│   ├── compress/              # Compression (zstd)
│   ├── pack/                  # Pack file format (multiple blobs per file)
│   ├── index/                 # Index management (blob -> pack mapping)
│   ├── tree/                  # Tree structures (directory metadata, file entries)
│   ├── snapshot/              # Snapshot metadata
│   ├── cache/                 # Local cache (encrypted at rest)
│   ├── prune/                 # Retention policies and garbage collection
│   ├── check/                 # Integrity verification
│   ├── lock/                  # Repository locking (exclusive/shared, stale detection)
│   ├── scheduler/             # Cron mode: schedule management, lock files
│   ├── config/                # YAML config loading, validation, secret resolution
│   ├── server/                # SFTP server mode (pkg/sftp RequestServer)
│   ├── notify/                # Notification system (command, webhook, email)
│   ├── whimsy/                # Funny messages, taglines, status text
│   └── tui/                   # Bubble Tea TUI
│       ├── app.go
│       ├── keys.go
│       ├── styles.go
│       ├── theme.go
│       └── views/
│           ├── dashboard.go   # Overview: status, warnings, witty greeting
│           ├── snapshots.go   # Browse/search snapshots (time machine)
│           ├── browser.go     # File tree browser within a snapshot
│           ├── backup.go      # Live backup progress (files, bytes, ETA)
│           ├── restore.go     # Restore target selection + progress
│           ├── prune.go       # Prune policy config + dry-run preview
│           ├── logs.go        # Recent cron run history
│           ├── health.go      # Destination connectivity + health
│           └── settings.go    # Backup configs, destinations, key management
│   ├── web/                   # Phase 2: Web UI
│   │   ├── server.go          #   net/http server, API routes, SSE, auth middleware
│   │   ├── auth.go            #   Session tokens, password auth, CSRF
│   │   └── api.go             #   JSON REST handlers
├── web/                       # Phase 2: Frontend SPA (TS + Tailwind, built by esbuild, embedded via embed.FS)
├── mise.toml
├── .goreleaser.yaml
├── .github/workflows/
│   ├── ci.yml
│   └── release.yml
└── go.mod
```

### Package Dependency Rules

- `types/` is dependency-free. All shared interfaces (`Backend`, `FileType`, `BlobID`, etc.) live here.
- Leaf packages (`crypto/`, `chunker/`, `compress/`, `pack/`, `index/`, `tree/`) import `types/` only.
- `repo/` orchestrates leaf packages. Leaf packages never import `repo/`.
- `backup/` and `restore/` import `repo/`. They never import each other.
- `backend/*` implementations import `types/` only.

---

## Configuration

### Format: YAML

YAML for its clean nested structures and readability. Go library: `gopkg.in/yaml.v3`. No Viper -- just `yaml.Unmarshal` directly into a Go struct with a `Validate()` method.

### Location

```
~/.config/doomsday/
├── client.yaml               # Client configuration (one config = one backup)
├── server.yaml                # Server configuration (only on server hosts)
├── master.key                 # Encryption key (generated by init)
└── state.json                 # Scheduler state (last run times, next due)
```

All files created with mode 0600/0700. Use `-c alt.yaml` to manage multiple independent backups.

### `client.yaml` (Client)

One config = one backup. Use `doomsday client -c alt.yaml` for separate backups.

```yaml
# ~/.config/doomsday/client.yaml

# key: env:DOOMSDAY_ENCRYPTION_KEY
# key: your-64-char-hex-key-here

sources:
  - path: ~/Documents
  - path: ~/Projects
    exclude: [node_modules, .git, vendor]

exclude:
  - .cache
  - "*.tmp"
  - .Trash

schedule: hourly

retention:
  keep_last: 5
  keep_hourly: 24
  keep_daily: 7
  keep_weekly: 4
  keep_monthly: 12
  keep_yearly: -1

destinations:
  - name: server
    type: sftp
    host: backup.example.com
    port: 8420
    user: laptop
    ssh_key: "base64-ed25519-key"
    host_key: "SHA256:xxxx"
    schedule: 4h
    retention:
      keep_daily: 30

  - name: usb
    type: local
    path: /mnt/usb-backup
    active: false
    schedule: weekly

  - name: b2
    type: s3
    endpoint: s3.us-west-004.backblazeb2.com
    bucket: my-doomsday-backups
    key_id: env:B2_KEY_ID
    secret_key: env:B2_APP_KEY

settings:
  compression: zstd
  compression_level: 3

notifications:
  policy: on_failure
  targets:
    - type: command
      command: "ntfy pub --title 'Doomsday: backup {{.Status}}' doomsday-alerts '{{.Message}}'"
    - type: webhook
      url: https://hooks.slack.com/services/XXX
```

### `server.yaml` (Server)

```yaml
# ~/.config/doomsday/server.yaml

data_dir: /var/lib/doomsday
host: 0.0.0.0
port: 8420
# host_key auto-generated on first serve if not specified
# tailscale_hostname: doomsday.tail1234.ts.net  # full FQDN; presence enables Tailscale
# tailscale_auth_key: tskey-auth-xxx            # for headless/unattended setup

clients:
  - name: laptop
    public_key: "ssh-ed25519 AAAA..."
    quota: 100GiB

  - name: desktop
    public_key: "ssh-ed25519 BBBB..."
    # quota: "0"  # unlimited (default)
```

Clients are added via `doomsday server client add <name>`, which generates an Ed25519 keypair, stores the public key in `server.yaml`, and prints the private key seed for the client.

### Secret References

Three ways to provide secrets in config values:

| Prefix | Example | Description |
|--------|---------|-------------|
| `env:` | `env:DOOMSDAY_B2_KEY` | Read from environment variable |
| `file:` | `file:/run/secrets/b2_key` | Read from file (for Docker/K8s secrets) |
| `cmd:` | `cmd:op read "op://vault/item/field"` | Execute command, use stdout (for 1Password, `pass`, macOS Keychain) |

**These prefixes work everywhere secrets appear**, including:
- `key` -- the master encryption key
- `destinations[].key_id`, `.secret_key` -- S3 backend credentials
- `destinations[].ssh_key` -- inline SSH private key seed

### Environment Variable Overrides

All sensitive config values can also be set via well-known environment variables, independent of the `env:` prefix in config. This enables **secret injection at runtime** via tools like `op run`, `knox`, `aws-vault`, `sops exec-env`, or Docker secrets:

| Environment Variable | Overrides |
|---------------------|-----------|
| `DOOMSDAY_KEY` | Encryption key (bypasses `key` config field) |
| `DOOMSDAY_<DEST>_KEY_ID` | `key_id` for destination `<DEST>` (uppercased name) |
| `DOOMSDAY_<DEST>_SECRET_KEY` | `secret_key` for destination `<DEST>` |
| `DOOMSDAY_<DEST>_SSH_KEY` | SSH private key seed for destination `<DEST>` |

**Precedence:** environment variable > `env:`/`file:`/`cmd:` in config > literal config value.

Example workflows:
```bash
# 1Password: inject all secrets at runtime
op run --env-file=.env.doomsday -- doomsday client backup

# Knox: decrypt secrets into env
knox env doomsday-prod -- doomsday client cron

# AWS Vault: assume role for S3 credentials
aws-vault exec backup-role -- doomsday client backup

# Simple: just export before running
export DOOMSDAY_KEY="correct horse battery staple"
doomsday client restore latest:/ --target /tmp/restore
```

**No secret ever needs to be stored in plaintext in `client.yaml`.** Between `env:`, `file:`, `cmd:`, and environment variable overrides, every deployment model is covered.

### SSH Host Key Pinning

Host keys are pinned per-destination via the `host_key` field (SHA256 fingerprint). No separate `known_hosts` file.

**Doomsday server destinations:** `doomsday server client add` prints the server's host key fingerprint. The fingerprint is stored in the destination's `host_key` field in the client config.

**Generic SFTP destinations:** The server's host key fingerprint is stored in the destination's `host_key` field in the YAML config.

**On subsequent connections:** The SFTP backend compares the server's key against the pinned fingerprint. A mismatch is a hard refusal. To rotate a server key, update the `host_key` field in the destination config.

---

## Repository Format

Each destination contains an independent repository (version 1). The repository stores encrypted, deduplicated, compressed data.

### Format Versioning

The repo `config` file contains a `version` integer. Rules:

- A binary refuses to open a repo with a version higher than it supports (prints "please update doomsday").
- A binary can open any repo with version <= its supported version.
- Version bumps only for breaking format changes. Minor additions use feature flags within the version.
- The client stores the last-seen repo version locally; refuses to open a repo whose version is lower than previously seen (rollback detection).

### Content-Addressable Storage

All data is split into content-defined chunks using **FastCDC** (Gear rolling hash with normalized chunking). Chunks are identified by their HMAC-SHA256 hash (keyed with a sub-key derived from the master key -- prevents confirmation-of-file attacks).

- **Algorithm:** FastCDC with normalized chunking (Xia et al., USENIX ATC '16)
- **Target chunk size:** 1 MiB (min 512 KiB, max 8 MiB)
- **Content ID:** HMAC-SHA256(content_id_key, chunk_data) -- keyed, not plain SHA-256
- **Deduplication:** global across all snapshots in the repository
- **Library:** `github.com/PlakarKorp/go-cdc-chunkers` (pure Go, keyed CDC support, actively maintained)
- **Immutable parameters:** algorithm ID, min/max/target size, gear table hash stored in repo config. Changing chunker = new repo.

### Pack Files

Blobs grouped into pack files (target ~16 MiB). Separate packers for data blobs vs tree blobs.

```
[EncryptedBlob1][EncryptedBlob2]...[EncryptedBlobN][EncryptedHeader][HeaderLength:4 bytes LE]
```

- Each blob independently encrypted (per-blob derived key via HKDF)
- Header at the END (streaming writes, no seek -- essential for S3/B2)
- Header (encrypted separately) maps blob IDs to offsets/lengths and blob types
- Pack file name = SHA-256 of full ciphertext contents
- `HeaderLength` is uint32 little-endian
- The `check` command verifies pack file names match SHA-256 of their contents

### Object Types

| Type | Contains |
|------|----------|
| **Data blob** | A chunk of file content |
| **Tree blob** | Directory listing: entries with name, metadata, blob references (JSON serialized) |
| **Snapshot** | Metadata: timestamp (backup start time), hostname, paths, tags, tree root, backup config name (JSON serialized) |

### Repository Layout

```
repo/
├── config              # Repo config (ID, version, chunker params) -- encrypted
├── keys/               # Key files (scrypt params + encrypted master key wrapper)
├── data/               # Pack files (data blobs + tree blobs, separate)
│   ├── 00/ .. ff/      # Two-char hex prefix subdirectories
├── index/              # Index files (blob ID -> pack + offset + type) -- encrypted
├── snapshots/          # Snapshot metadata files -- encrypted
└── locks/              # Lock files (JSON: hostname, PID, timestamp, type, HMAC)
```

---

## Encryption

### Key Hierarchy with Domain Separation

```
User Password (or 24-word recovery phrase)
    |
    v  scrypt (params stored in key file: {N, r, p, salt})
Key Encryption Key (KEK)
    |
    v  AES-256-GCM unwrap
Master Key (256-bit, generated via crypto/rand at init)
    |
    +--HKDF(master, "doomsday-data-v1")------> Data Encryption Sub-Key
    +--HKDF(master, "doomsday-tree-v1")------> Tree Encryption Sub-Key
    +--HKDF(master, "doomsday-index-v1")-----> Index Encryption Sub-Key
    +--HKDF(master, "doomsday-snapshot-v1")--> Snapshot Encryption Sub-Key
    +--HKDF(master, "doomsday-config-v1")----> Config Encryption Sub-Key
    +--HKDF(master, "doomsday-content-id")---> Content ID Key (for HMAC-SHA256 blob IDs)
```

Each blob type uses its own sub-key. Cross-context ciphertext substitution is impossible.

### Per-Blob Encryption

Each blob is encrypted with a **per-blob derived key**:

```
blob_key = HKDF-SHA256(sub_key, blob_id)  // unique key per blob
nonce = random 96 bits from crypto/rand
ciphertext = AES-256-GCM(blob_key, nonce, plaintext, AAD)
```

**AAD (Authenticated Associated Data)** bound to every encryption:
- Blob ID (HMAC-SHA256 of content)
- Blob type (data, tree, index, snapshot, config)
- Repository ID

This prevents: moving blobs between repos, swapping blob types, substituting one blob for another. Nonce collision is impossible since each key is used exactly once.

### Key File Format

```json
{
  "version": 1,
  "kdf": "scrypt",
  "N": 131072,
  "r": 8,
  "p": 1,
  "salt": "<base64>",
  "nonce": "<base64>",
  "wrapped_master_key": "<base64>"
}
```

Default scrypt: N=2^17, r=8, p=1 (~128 MiB RAM, ~0.5s on modern hardware). Deployable on Raspberry Pi. Parameters stored per key file -- can be tuned per key without breaking others.

### Default Key Generation

On `doomsday client init`, generate a **24-word BIP39 recovery phrase** (256 bits of entropy from a 2048-word list). This IS the key (bypasses scrypt -- scrypt is only for user-chosen passwords).

```
╔══════════════════════════════════════════════════════════════╗
║  YOUR BACKUP ENCRYPTION KEY -- SAVE THIS NOW                 ║
║                                                              ║
║  repair blanket dolphin sunset archive crystal quantum       ║
║  harbor pencil thunder vintage morning glacier phantom       ║
║  compass rhythm whisper fortune cascade emerald oxygen       ║
║  nebula prism voyage                                         ║
║                                                              ║
║  24 words. 256 bits. Without this, your backups are GONE.    ║
║  Write it down. Store it somewhere safe. Do not lose it.     ║
╚══════════════════════════════════════════════════════════════╝
```

### Memory & Key Material

Go cannot guarantee memory zeroing (GC copies allocations). The spec is honest about this:
- Master key held in `[32]byte` on stack where possible, explicitly zeroed after use
- Recommend: `ulimit -c 0` (no core dumps), encrypted swap
- On Linux: `syscall.Mlockall(MCL_CURRENT|MCL_FUTURE)` if `CAP_IPC_LOCK` is available (works without CGO)

---

## Compression

- **Algorithm:** zstd (via `github.com/klauspost/compress/zstd`, pure Go)
- **Applied per-chunk** before encryption
- **Auto-detection:** skip compression if compressed size >= 97% of original
- **Compression level:** configurable, default 3

---

## Backup Engine -- The Pipeline

Six-stage concurrent pipeline. Bounded channels between stages provide natural backpressure.

```
Walk -> Change Detect -> File Save (read+CDC) -> Dedup -> Compress+Encrypt -> Pack -> Upload
            |                                      |
          unchanged                           already exists
          reuse node                            skip blob
```

### Stage Details

| Stage | Workers | Bounded By |
|-------|---------|-----------|
| Walk + stat | 1 goroutine | Filesystem I/O |
| Change detector | 1 goroutine | Previous snapshot tree |
| File saver (read + FastCDC) | 2 goroutines | Disk read I/O |
| Dedup check | Inline with file saver | Index lookup (in-memory) |
| Compress + encrypt | Inline with file saver | CPU |
| Packer (accumulate ~16 MiB) | 1 per blob type (data, tree) | Memory |
| Uploader | N goroutines (SFTP: 4, S3: 10, local: unbounded) | Network/disk I/O |
| Tree saver | GOMAXPROCS+2 goroutines | CPU |

### Critical Design Rules

1. **`SaveBlobAsync()` must NOT block on upload.** Completed packs handed to upload pool in background. When upload queue is full, packer blocks, which causes compress to block, which causes file saver to block. This is correct backpressure.

2. **Dedup check is atomic.** `MasterIndex.CheckAndAdd(blobID)` is a single mutex-protected check-and-set. Returns `true` if this call added it (proceed to store), `false` if already known (skip). No separate Has() + AddPending().

3. **Tree saver uses per-directory futures.** Each directory has a `FutureTree` that resolves when all children are complete. Walk is top-down; tree finalization is bottom-up. The tree saver waits on child futures before building the parent tree blob.

4. **Snapshot finalization order is strict:**
   - (a) Flush all data packs, wait for upload completion
   - (b) Flush all tree packs, wait for upload completion
   - (c) Write index files to backend
   - (d) Write snapshot file to backend
   - A crash at any point before (d) = no snapshot written = orphaned packs cleaned up by `prune`

5. **Lifecycle via `errgroup` with context cancellation.** Any stage error cancels everything. Channels between stages; errgroup for lifecycle. `SetLimit()` controls concurrency within a stage.

6. **Memory budget** (default config, moderate 1 TB repo):
   - Pipeline in-flight: ~176 MiB
   - Index: ~100 MiB (see Index Memory Strategy below)
   - Packer + upload buffers: ~192 MiB
   - Total: ~500-700 MiB. Documented honestly, not understated.

7. **Graceful shutdown (SIGINT/SIGTERM):** Cancel context. In-flight uploads complete or abort. No snapshot file written. Lock released. Orphaned packs cleaned by next prune. TUI restores terminal.

### Change Detection

Fast path: same `size` + `mtime` + `ctime` + `inode` -> assume unchanged, reuse prior chunks.

**Known limitations** (documented):
- NFS: inode may be unstable across remounts. Use `--force` for NFS paths.
- FUSE/Docker volumes: stat behavior varies. Use `--force`.
- FAT32/exFAT: 2-second mtime granularity. Use `--force`.
- Windows: `ctime` means creation time, not change time. Detection uses `size` + `mtime` only.
- Files modified during backup: re-stat after read. If stat changed, warn and re-read once. If still changing, warn and include as-read (exit code 3).

### File Metadata Preserved

Permissions, ownership (uid/gid with name mapping), timestamps (mtime, atime, ctime), extended attributes (xattrs, including POSIX ACLs), symlink targets, hardlinks (same inode -> store once, record links), sparse file detection, special files (devices, FIFOs -- metadata only).

### Exclusion Rules

- Glob patterns per backup config (`exclude` array)
- External exclude file (`exclude_file`)
- `.doomsdayignore` files in directories (`.gitignore` syntax)
- Built-in defaults: skip `/proc`, `/sys`, `/dev`
- Max file size limit (optional, `max_file_size` in config)

---

## Restore

### Guarantees

1. **Per-file atomic writes:** each file written to `.doomsday.tmp.<random>` suffix, renamed to final name only after full content written and verified. Crash during restore never leaves half-written files.

2. **Content verification:** after decrypting each blob, verify HMAC-SHA256 matches the blob ID. Catches index corruption (wrong offset pointing to wrong data).

3. **Write ordering:** directories created first. Files written. Hardlinks created after their target files exist. Permissions and ownership set after all files written (directories may need write permission during restore). Timestamps set last (writing into directory changes mtime).

4. **Sparse file restoration:** if original was sparse, restore recreates holes via `SEEK_HOLE`/`SEEK_DATA` or `fallocate(FALLOC_FL_PUNCH_HOLE)` where supported.

5. **Concurrent restore + prune protection:** restore acquires a shared lock. Prune acquires an exclusive lock. They cannot run simultaneously.

---

## Prune & Retention

### Retention Policies

Per backup config. `-1` = keep forever. `max_size` is a hard cap that overrides all keep rules (documented: this CAN delete snapshots protected by keep rules if the repo exceeds the size limit).

```yaml
retention:
  keep_last: 5
  keep_hourly: 24
  keep_daily: 7       # "daily" boundaries in UTC
  keep_weekly: 4
  keep_monthly: 12
  keep_yearly: -1
  keep_within: 30d
  max_size: 500GiB    # hard cap, overrides keep rules
```

### `doomsday client forget` vs `doomsday client prune`

**`forget`** removes snapshot metadata only. Fast. Does not reclaim space.
**`prune`** runs forget (if needed), then garbage collects unreferenced packs.

Users can `doomsday client forget abc123` to manually remove a specific snapshot, then `doomsday client prune` later to reclaim space.

### Prune Algorithm (crash-safe ordering)

```
1. Apply retention rules, mark snapshots for removal
2. Walk ALL kept snapshots, build referenced blob set
3. Classify packs:
     liveness == 0.0  -> garbage
     liveness < 0.80  -> repack candidate (budget-limited, worst first)
     liveness >= 0.80 -> keep as-is
4. Write "prune intent" file listing packs to delete
5. Repack: read live blobs from candidates, write NEW packs
6. Write NEW index files referencing new packs (and excluding removed blobs)
7. Delete OLD index files
8. Delete old packs + garbage packs
9. Delete prune intent file
10. Verify integrity (structure check)

On startup: if prune intent file exists, resume from step 6.
Crash at any point is recoverable.
```

**Invariant:** prune NEVER deletes a pack that is referenced by any kept snapshot. The reachability analysis must complete before any deletion begins.

**Concurrent backup + prune is prevented by exclusive locking.**

---

## Locking

### Design

Lock files stored in `locks/` directory of the repository. Each lock is a JSON file containing:

```json
{
  "hostname": "macbook.local",
  "pid": 12345,
  "created": "2026-03-19T14:22:01Z",
  "refreshed": "2026-03-19T14:23:01Z",
  "type": "exclusive",
  "operation": "backup",
  "hmac": "<HMAC-SHA256 of above fields with content_id_key>"
}
```

### Lock Types

- **Exclusive** (backup, prune, forget): only one at a time.
- **Shared** (check, restore, snapshots, ls, browse): multiple concurrent allowed. Blocked by exclusive.

### Protocol

1. Write lock file to `locks/<random-id>.json`
2. List all lock files in `locks/`
3. Check for conflicts (exclusive vs exclusive, exclusive vs shared)
4. If conflict: check staleness. A lock is stale if:
   - `refreshed` timestamp is older than 30 minutes, OR
   - HMAC verification fails (injected lock from attacker)
5. If stale: remove and retry. If not stale: abort with message.
6. Active locks refresh their timestamp every 60 seconds.

### Backend-Specific Notes

- **Local:** additionally use `flock()` on a single file for true mutual exclusion.
- **SFTP/S3:** advisory locks only. Document the race window (two processes can both see no conflict if they interleave). Both operations are crash-safe, so worst case is duplicate work, not corruption.

`doomsday client unlock` removes all locks. Auto-stale detection in `doomsday client cron` removes locks from crashed processes automatically.

---

## Integrity Verification (`doomsday client check`)

Three levels:

1. **Structure check (fast):** verify indexes, snapshot->tree->blob references. No data download.
2. **Pack header check (medium):** download headers, verify against index entries. Check pack names match SHA-256 of contents.
3. **Full data check (slow, `--read-data`):** decrypt every blob, verify HMAC-SHA256 matches blob ID.

On failure: report affected snapshots/files, whether repair is possible. Cron mode runs structure check weekly by default.

---

## Backends

### Interface

```go
// Defined in internal/types/
type Backend interface {
    Location() string
    Save(ctx context.Context, t FileType, name string, rd io.Reader) error
    Load(ctx context.Context, t FileType, name string, offset, length int64) (io.ReadCloser, error)
    Stat(ctx context.Context, t FileType, name string) (FileInfo, error)
    Remove(ctx context.Context, t FileType, name string) error
    List(ctx context.Context, t FileType, fn func(FileInfo) error) error
    Close() error
}
```

### Local Filesystem
Simplest backend. Repository is a directory on disk.

### SFTP
- Uses `pkg/sftp` (pure Go, CGO=0). Both client and server.
- Supports SSH agent, key files, password auth. Connection pooling.
- Same backend talks to any SFTP host or doomsday server identically.

### S3-Compatible (Backblaze B2, MinIO, Wasabi, Cloudflare R2, etc.)
- Uses `github.com/minio/minio-go` (lighter than `aws-sdk-go-v2`, pure Go, S3-compatible).
- Configurable `endpoint` URL -- B2 is a preset, but any S3-compatible store works.
- Concurrent multipart uploads for large packs.
- Credentials via `env:`, `file:`, or `cmd:` secret references.

### Server Mode (SFTP)

The doomsday server is an SFTP server using `pkg/sftp` `RequestServer`. One protocol for everything.

**Client generates its own SSH keypair.** User provides the public key to `doomsday server add <client> --pubkey <path>`. The server never sees the private key.

**SFTP handler security (whitelist approach):**
- Implement `FileReader`, `FileWriter` (new files only), `FileLister`.
- Return `ErrSSHFxOpUnsupported` for everything else by default.
- `FileCmder`: only allow `Mkdir`. Reject `Remove`, `Rmdir`, `Rename`, `Setstat` (prevents truncation attacks).
- Resolve symlinks in all paths, verify real path within jail. Reject hard link creation.
- Quota: atomic reservation (decrement before write begins, increment on failure). Mutex-protected.
- Each client jailed to `<data-dir>/<client-name>/`.

**Rollback detection:** client stores locally the most recent snapshot ID per destination. On connection, verifies remote snapshot list includes all previously-seen IDs. Missing = loud alarm.

---

## Index Memory Strategy

For a 1 TB repo (~1M blobs), the index is ~100 MiB. For 100 TB (~100M blobs), it's ~10 GB.

Strategy (tiered, like restic's ongoing improvements):

1. **Phase 1:** Load full index into memory. Sufficient for repos up to ~10 TB on modern machines. Document memory requirements per repo size.
2. **Phase 1 optimization:** Bloom filter as first-pass dedup check (false positives trigger disk lookup, false negatives impossible). Dramatically reduces memory for the common case.
3. **Future:** Memory-mapped sorted index files with binary search. On-disk index with in-memory hot cache.

---

## Error Handling

### Philosophy

- **Fatal errors:** wrong password, corrupted repo config, lock contention, backend unreachable after retries. Abort immediately.
- **Non-fatal errors:** permission denied on individual files, single file changed during read. Continue backup, record warning, exit code 3.
- **Retryable errors:** network timeouts, transient backend failures. Retry with exponential backoff (3 attempts, 1s/5s/30s). Then fail.

### Error Summary (headless mode)

On completion with warnings/errors, print a structured summary:

```
 14:25:14 WRN  Backup completed with warnings
         ├── 3 files could not be read (permission denied)
         │   /etc/shadow
         │   /var/lib/private/secret
         ├── 1 file changed during read (backed up as-read)
         │   /var/log/syslog
         └── Destination b2: OK | Destination nas: OK
```

---

## CLI Commands Reference

| Command | Description |
|---------|-------------|
| **Client commands** (`doomsday client`) | |
| `doomsday client` | Show backup status overview (default action) |
| `doomsday client init` | Interactive setup wizard |
| `doomsday client backup [--dry-run]` | Run backup to all active destinations |
| `doomsday client restore <snap[:path]> -t <dir>` | Restore files |
| `doomsday client snapshots` | List snapshots |
| `doomsday client ls <snap[:path]> [--long]` | Browse snapshot contents |
| `doomsday client find <pattern>` | Search files across snapshots |
| `doomsday client diff <snap1> <snap2>` | Show changes between snapshots |
| `doomsday client status [--check] [--fix]` | Health overview (absorbs old doctor + stats) |
| `doomsday client check [--level ...]` | Verify repository integrity |
| `doomsday client forget <snap-id...>` | Remove specific snapshots |
| `doomsday client prune [--dry-run] [--yes]` | Apply retention + garbage collect |
| `doomsday client unlock` | Break stale locks |
| `doomsday client key add \| remove \| list` | Encryption key management |
| `doomsday client cron` | One scheduler pass |
| `doomsday client cron install \| uninstall \| status` | Platform scheduler management |
| **Server commands** (`doomsday server`) | |
| `doomsday server` | Show server status (default action) |
| `doomsday server init` | Create server.yaml config |
| `doomsday server serve` | Start SFTP server |
| `doomsday server install \| uninstall` | System daemon management |
| `doomsday server status` | Daemon + client info |
| `doomsday server client add <name> [--quota]` | Generate keypair, print one-liner |
| `doomsday server client remove <name>` | Unregister client |
| `doomsday server client list` | List clients |
| **Global** | |
| `doomsday version` | Show version |
| `doomsday update` | Self-update from GitHub releases |

Client commands accept: `-c`/`--config`, `--json`, `--verbose`, `--quiet`, `--no-whimsy`
Server commands accept: `-c`/`--config`, `--json`

---

## Cron Mode

```bash
# Install the system scheduler (detects OS automatically):
doomsday client cron install

# Or manual crontab:
*/15 * * * * /usr/local/bin/doomsday client cron
```

**Doomsday manages its own schedule.** The system scheduler just wakes it up.

On each invocation:
1. Acquire lock (if locked, exit cleanly)
2. Read state.json for last run times
3. Run each due backup config (respecting schedule field)
4. After backup: auto-prune if due (weekly by default)
5. After prune: auto-check if due (weekly, structure-level)
6. Update state.json
7. On any failure: trigger notifications
8. Release lock

**Missed wakeups:** if the machine was asleep for 8 hours, run each config once if due. Never batch-run multiple missed windows.

**Network awareness:** skip SFTP/S3 destinations if network is unreachable. Log as skip, not error. Retry next wakeup.

Logs to `~/.local/state/doomsday/cron.log` (with rotation, max 10 MiB, keep 5 rotated files).

---

## Service Installation

### Client: `doomsday client cron install`

Already described above. Detects the platform and installs the appropriate scheduler:

| Platform | Mechanism | Notes |
|----------|-----------|-------|
| Linux (systemd) | User-level systemd timer (`~/.config/systemd/user/doomsday.timer` + `.service`) | Runs as current user. `systemctl --user enable --now doomsday.timer`. |
| macOS | launchd plist (`~/Library/LaunchAgents/com.doomsday.cron.plist`) | `launchctl load`. Runs as current user. |
| Windows | Task Scheduler | `schtasks /create`. Runs as current user at logon + every 15 min. |
| Other / unsupported | Prints manual crontab line | `*/15 * * * * /path/to/doomsday client cron` |

`doomsday client cron uninstall` reverses the above. `doomsday client cron status` checks if the scheduler entry exists and is active.

### Server: `doomsday server install`

Installs a **system-level** daemon for the doomsday SFTP server. This is the server side — it should run at boot, not tied to a user session.

| Platform | Mechanism | Notes |
|----------|-----------|-------|
| Linux (systemd) | System unit (`/etc/systemd/system/doomsday-server.service`) | Requires `sudo`. `Type=notify`, `Restart=on-failure`, hardened with `ProtectSystem=strict`, `PrivateTmp=yes`, `NoNewPrivileges=yes`. Runs as dedicated `doomsday` user (created if missing). |
| macOS | launchd daemon (`/Library/LaunchDaemons/com.doomsday.server.plist`) | Requires `sudo`. Runs as dedicated `_doomsday` user. `KeepAlive=true`. |
| Other | Fail with actionable message | "Server install is only supported on Linux (systemd) and macOS (launchd). Run `doomsday server` manually or write your own init script." |

**What `server install` does:**

1. Check for root/sudo (fail clearly if missing)
2. Create dedicated service user (`doomsday` / `_doomsday`) if it doesn't exist
3. Create data directory (`/var/lib/doomsday/`) owned by service user
4. Write the unit file / plist, pointing to the current binary path
5. Enable and start the service
6. Print status confirmation

**What it does NOT do:** configure clients, generate keys, or touch the repo. That's `doomsday server client add` and friends.

`doomsday server uninstall` stops the service, removes the unit/plist, and optionally removes the service user (asks first — data directory is preserved).

`doomsday server status` shows: running/stopped, uptime, listening address, registered clients, last connection times.

---

## Status (`doomsday client status`)

The "is my setup actually going to work?" command. Shows health overview by default; use `--check` for comprehensive diagnostics.

```
$ doomsday client status --check

  Doomsday Health Check
  ─────────────────────

  Configuration
  ✓ Config file found (~/.config/doomsday/client.yaml)
  ✓ Config parses cleanly
  ✓ 2 destinations defined (server, nas)
  ✓ Encryption key accessible

  Destinations
  ✓ server (sftp://laptop@backup.example.com:8420) .. connected, 1.2 TiB free
  ✓ nas (sftp://backup@nas.local) ................... connected, 3.4 TiB free
  ⚠ local (/mnt/backup-drive) ...................... not mounted

  Repositories
  ✓ server ........................................... v1, 847 snapshots, healthy
  ✓ nas .............................................. v1, 312 snapshots, healthy
  - local ............................................ skipped (not mounted)

  Scheduler
  ✓ Cron installed (launchd, active)
  ✓ Last run: 12 minutes ago (success)

  System
  ✓ Binary: /usr/local/bin/doomsday v0.4.2 (up to date)
  ✓ Permissions: config dir 0700, key files 0600
  ✓ Disk space: 48 GiB free on cache volume
  ⚠ ulimit -c is not 0 (core dumps could leak key material)

  3 passed, 2 warnings, 0 failed
```

### Checks Performed

| Category | Checks |
|----------|--------|
| **Configuration** | Config file exists and parses. All referenced files/dirs exist. Secret references resolve (`env:` vars set, `cmd:` commands succeed, `file:` paths readable). No unknown keys (catches typos). At least one destination. Retention policies are sane (warns on `keep_last = 0`). |
| **Destinations** | Each destination is reachable (SFTP connect, S3 `HeadBucket`, local path exists + writable). Reports free space / quota. SSH host key matches pinned fingerprint. |
| **Repositories** | Each reachable destination has a valid repo. Repo version is supported. No stale locks. Last `check` result (if available). Warns if repo has never been checked. |
| **Scheduler** | Cron/timer/plist installed and active. Last run time and result. Overdue backup detection. |
| **System** | Binary version (update available?). Config dir permissions. Cache dir writable with sufficient space. Core dump settings. Swap encryption (Linux: checks `/proc/swaps` for encrypted swap). |

### Flags

```
doomsday client status              # health overview (last backup, next due, repo sizes)
doomsday client status --check      # full diagnostic checkup
doomsday client status --check --json  # machine-readable output
doomsday client status --fix        # attempt to fix simple issues (permissions, missing dirs, stale locks)
```

`--fix` is conservative: it fixes file permissions, creates missing directories, removes stale locks, and suggests commands for everything else. It never modifies config or touches repo data.

---

## Whimsy

A pool of funny messages for dashboard greetings, backup start/complete, idle status. Selected randomly with day-seeded RNG (consistent within a session, fresh each day).

**Rules:**
- **Never in error output.** Errors are clinical and actionable.
- **Reduced in cron mode.** One greeting line, no completion quip.
- **Disabled with `whimsy = false` in config or `--no-whimsy` flag.**
- **Match the brand.** Apocalyptic/paranoid tone. "Your data survived another day" fits. "Your bits are tucked in safe and sound" does not.

---

## TUI Views

Follow Bubble Tea / Lipgloss patterns from Go CLI/TUI conventions (Elm architecture, adaptive colors, vim keybindings, `?` help, `:` command palette).

| View | Key | Purpose |
|------|-----|---------|
| Dashboard | (default) | Status overview, warnings, witty greeting, all configs |
| Snapshots | `enter` on a config | Browse/search snapshots, time machine |
| File Browser | `enter` on a snapshot | Tree browser within a snapshot |
| Backup Progress | `b` | Live progress bars, speed, ETA |
| Restore Progress | `r` | Same treatment as backup |
| Logs | `l` | Recent cron run history (color-coded status) |
| Health | `h` | Destination connectivity, disk/quota usage, last check |
| Diff | `d` (two snapshots selected) | Tree of added/removed/modified files |
| Settings | `c` | Edit configs, destinations, keys (Huh forms) |
| Help | `?` | All keybindings for current view |

---

## Web UI (Phase 2)

An embedded web interface for when you want the paranoia of a terminal backup tool but the comfort of a browser. Single binary -- no `npm install`, no Node runtime. Just `doomsday web` and you're in.

### Stack

- **Backend:** Go `net/http` server, JSON REST API, SSE for live progress
- **Frontend:** Vanilla TypeScript SPA, Tailwind CSS, built at release time, embedded via `embed.FS`
- **No React.** No framework. The UI is a backup dashboard, not a social network. Vanilla TS + Tailwind keeps the binary small and the build simple. Web components for encapsulation where needed.
- **Build:** `esbuild` bundles TS + Tailwind at release time. CI compiles the SPA, Go embeds the `dist/` output. Zero runtime JS dependencies.

### Security: Localhost-Only by Default

```
doomsday web                    # listen 127.0.0.1:8666
doomsday web --port 9000        # listen 127.0.0.1:9000
doomsday web --host 0.0.0.0     # listen on all interfaces (requires --password)
```

**Binding to non-loopback addresses requires `--password` or config password.** The binary refuses to start otherwise. No footguns.

### Authentication

Three modes, depending on context:

| Mode | When | How |
|------|------|-----|
| **Auto-open** | Desktop, no `--password` | Generate random 32-char session token, pass as URL fragment (`http://127.0.0.1:8666/#token=...`). Open browser automatically via `xdg-open` / `open` / `start`. Token shown in terminal as fallback. |
| **Random password** | Headless/SSH, no `--password` | Generate random password, print to terminal once. User enters it in the browser login form. |
| **Configured password** | `--password`, `web.password` in config, or `env:DOOMSDAY_WEB_PASSWORD` | Use the provided password. Required for non-localhost binding. |

Session: `HttpOnly`, `Secure` (when TLS), `SameSite=Strict` cookie. 24-hour expiry. CSRF token on all mutating endpoints.

### TLS

- `doomsday web --tls` generates a self-signed cert on first run (stored in config dir). Browsers will warn, but the connection is encrypted.
- `doomsday web --tls-cert <cert> --tls-key <key>` for real certs (e.g. from Let's Encrypt or Tailscale).
- Without `--tls`, only listens on localhost. Binding to 0.0.0.0 without TLS prints a scary warning.

### Views

#### Dashboard
The landing page. Everything you need at a glance.
- All backup configs with last run time, next scheduled, status (ok / overdue / failing)
- Storage usage per destination (bar charts, color-coded)
- Recent activity feed (last N backup/prune/check events)
- Whimsy greeting (of course)

#### Configuration Editor
- Add/edit/remove destinations (test connectivity inline)
- Add/edit/remove backup configs (path picker, exclusion builder, retention sliders)
- Key management (add password, view recovery phrase, remove keys)
- Global settings (compression, bandwidth limits, theme, whimsy toggle)
- Changes write back to `client.yaml` with comments preserved

#### Snapshot Browser (Time Machine Mode)
The crown jewel. Browse your backups like macOS Time Machine.
- **Timeline scrubber:** horizontal timeline showing all snapshots for a backup config. Scroll through time. Snapshots cluster by density (hourly dots collapse into daily markers at distance).
- **File tree:** hierarchical browser showing the directory tree at the selected point in time. Expand/collapse directories. File metadata on hover (size, modified, permissions).
- **Diff overlay:** select two snapshots (or "now" vs a snapshot) to see added/removed/modified files highlighted in the tree. Green/red/yellow.
- **Restore from here:** right-click any file or directory to restore it. Pick target location. Shows progress inline.
- **Search:** fuzzy filename search across the selected snapshot. `Ctrl+F` or `/`.

#### Backup / Restore
- **Trigger backup:** select config, hit "Back Up Now". Live progress with SSE -- files processed, bytes transferred, speed, ETA, current file path.
- **Trigger restore:** from snapshot browser, select files/dirs, choose target, go. Same live progress treatment.
- **Cancel:** abort button sends context cancellation.

#### Health & Stats
- Destination connectivity status (green/yellow/red)
- Repo integrity (last check result, time since last check)
- Storage breakdown (data vs index vs snapshots, dedup ratio, compression ratio)
- Cron history log (tabular, color-coded, filterable)

### API

All UI actions go through a JSON REST API. The API is internal (not a public contract), but cleanly structured for potential future use.

```
GET    /api/status                    # dashboard data
GET    /api/backups                   # list backup configs
POST   /api/backups/:name/run         # trigger backup
GET    /api/backups/:name/snapshots   # list snapshots
GET    /api/backups/:name/snapshots/:id/tree?path=/  # browse tree
GET    /api/backups/:name/snapshots/:id/diff/:other  # diff two snapshots
POST   /api/restore                   # trigger restore (body: snapshot, paths, target)
GET    /api/destinations              # list destinations
POST   /api/destinations/:name/test   # test connectivity
GET    /api/config                    # current config (secrets redacted)
PUT    /api/config                    # update config
GET    /api/health                    # destination health + repo stats
GET    /api/events                    # SSE stream (backup progress, status changes)
```

### Design Language

- **Clean, minimal, dark-mode-first.** Light mode available. Respects `prefers-color-scheme`.
- **Tailwind utility classes.** No custom CSS framework. Consistent spacing, typography, colors.
- **Monospace accents** for paths, blob IDs, timestamps. Sans-serif for everything else.
- **Apocalyptic-chic.** Subtle doomsday branding -- muted reds and ambers for warnings, the logo watermark, whimsy messages woven into empty states.
- **Responsive.** Works on a phone (check your backups from the couch) but optimized for desktop.
- **Keyboard navigable.** Tab order, focus rings, `k`/`j` navigation in lists. Accessible.

---

## Testing Strategy

**Correctness is existential.** The test suite must be relentless.

### Unit Tests (per package, table-driven, `-race`)

Every package listed in the architecture gets thorough unit tests. Key areas: crypto roundtrips, nonce uniqueness, chunker determinism/boundary stability, pack format read/write, index lookup, tree serialization, retention policy math, config parsing/validation, SFTP handler enforcement, lock stale detection.

### Property-Based / Fuzz Tests

- Chunker: re-chunking identical input = identical chunks. Concatenation = original.
- Crypto: decrypt(encrypt(x)) == x. Any bit flip = authentication failure.
- Pack reader: fuzz with random bytes. Never panic, always clean error.
- Tree serialization: fuzz with random metadata. Lossless roundtrip.
- Config parser: fuzz with random YAML. Never panic.
- SFTP handler: fuzz with malformed requests, path traversals.

### Integration Tests

- Full backup-restore cycle: byte-identical after roundtrip.
- Incremental: only changed chunks are new. Both snapshots restore correctly.
- Multi-destination: partial failure semantics (A succeeds, B fails).
- Prune safety: prune then restore remaining snapshots.
- Crash recovery: kill process mid-backup, verify repo consistent. Kill mid-prune, verify intent file resumes.
- Concurrent access: locking prevents corruption.
- Server readonly: raw SFTP client attempts every prohibited operation.
- Cron mode: simulated clock, schedule adherence, lock overlap prevention.
- Backend failures: `FailingBackend` wrapper injects errors at configurable points.

### Backward Compatibility Tests

Golden test repositories (one per format version) checked into test fixtures. Every new version must open, read, restore from, and prune these repos. Catches accidental format breaks.

### Soak Tests (nightly CI)

Hundreds of incremental backups with random mutations. Prune repeatedly. Check integrity every N operations. Monitor memory over time. Run for hours.

### Benchmark Tests with Targets

| Metric | Target |
|--------|--------|
| FastCDC throughput | >= 2 GB/s single-core |
| AES-256-GCM throughput | >= 3 GB/s (AES-NI) |
| zstd level 3 | >= 500 MB/s |
| End-to-end backup (local, initial) | >= 200 MiB/s |
| Incremental backup (100K files, few changes) | < 5 seconds |
| Restore (local) | >= 200 MiB/s |
| Memory (1 TB repo, defaults) | <= 700 MiB peak |

---

## Build, Release & Distribution

### CI (every push/PR)

Multi-platform matrix (ubuntu, macos, windows). Lint (`go vet`, `staticcheck`), test (`-race -cover`), build, `govulncheck`, `gosec`. Coverage reported via Codecov.

### Release (on `v*` tags)

GoReleaser builds:
- **Binaries:** linux/darwin/windows x amd64/arm64 (6 targets)
- **Docker:** multi-arch images on `ghcr.io/jclement/doomsday`
- **Homebrew:** `jclement/homebrew-tap`
- **Signed:** cosign (Sigstore) keyless signing with transparency log. Checksums file signed.
- **Self-update:** `creativeprojects/go-selfupdate` verifies cosign signature before applying. Update check opt-in (enable in config or TUI).

---

## Security Model

| Threat | Mitigation |
|--------|-----------|
| Server compromise | E2E encryption -- server never sees plaintext |
| Client compromise (ransomware) | Append-only server mode |
| Confirmation-of-file attack | Keyed content IDs (HMAC-SHA256, not plain SHA-256) |
| Ciphertext substitution | Per-blob-type sub-keys via HKDF + AAD binding |
| Nonce reuse | Per-blob derived keys -- each key used exactly once |
| Password brute-force | scrypt with configurable cost parameters |
| Tampering with stored data | AES-GCM authentication tag |
| Rollback attack | Client-side monotonic snapshot tracking |
| Repository downgrade | Client refuses repo version lower than previously seen |
| Supply chain (malicious binary) | Cosign-signed releases, verified self-update |
| Lock file DoS | HMAC-authenticated locks, stale timeout |
| Metadata in local cache | Cache encrypted at rest |
| Memory disclosure | Documented Go limitations. Mlockall where possible. |
| B2 credential theft | `env:` / `file:` / `cmd:` + env var overrides -- never plaintext in config |
| Web UI exposure | Localhost-only default, password required for non-loopback, CSRF tokens, HttpOnly cookies |

### Things We Don't Roll Our Own

AES-256-GCM (Go stdlib), HKDF-SHA256 (`golang.org/x/crypto`), scrypt (`golang.org/x/crypto`), HMAC-SHA256 (Go stdlib), crypto/rand, zstd (well-tested pure Go library).
