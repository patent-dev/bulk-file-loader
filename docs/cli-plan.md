# CLI Tool Plan

## Overview

Single binary, two modes. `bulk-file-loader serve` starts the web UI server (existing behavior). All other commands operate directly on the data directory without a running server.

Both modes share the same core: config, database, source registry, downloader. The server additionally runs the HTTP layer, scheduler, and webhook delivery.

Binary name: `bulk-file-loader` (matches repo, Docker image, Dockerfile ENTRYPOINT).

## Design principles

- **Scriptable first**: machine-readable output (json, csv), pipeable IDs (`-q`), proper exit codes
- **ETL-friendly**: data engineers integrate this into existing pipelines, monitoring, and security infrastructure
- **Standalone capable**: no server required for one-shot downloads; direct mode avoids extra HTTP hop through own server (relevant in corporate networks with gateway timeouts)
- **Auditable**: all download attempts recorded in DB with timestamps, checksums, errors; `download history` and `file show` provide full audit trail
- **Composable**: small commands that chain well; `pull` is convenience sugar over `product sync --all` + `download all`

## Exit codes

```
0   Success
1   General error (invalid arguments, DB error, etc.)
2   Authentication required (passphrase not configured or invalid)
3   Partial failure (some downloads failed, some succeeded)
```

`pull` and `download all` return exit code 3 if at least one file failed but others succeeded. Scripts can check `$?` to decide whether to retry.

## Architecture

```
bulk-file-loader/
  cmd/
    bulk-file-loader/
      main.go           # entry point, root cobra command
      serve.go          # serve subcommand (current main.go logic)
      setup.go          # first-time passphrase configuration
      source.go         # source ls/show/enable/disable/test
      product.go        # product ls/show/sync/enable/disable
      file.go           # file ls/show/download/cancel/skip/unskip/reset/delete
      download.go       # download history/all
      pull.go           # sync + download all
      webhook.go        # webhook ls/add/update/rm
      status.go         # status summary
      output.go         # shared table/json/csv formatting
  internal/
    core/
      core.go           # shared initialization with options
    sync/
      sync.go           # extracted synchronous sync logic (shared by CLI and scheduler)
```

### Core initialization

`internal/core/core.go` extracts the shared setup from `main.go`:

```go
type Core struct {
    Config     *config.Config
    DB         *database.DB
    Auth       *auth.Service
    Registry   *sources.Registry
    Downloader *downloader.Downloader
    Hooks      *hooks.Manager
    Scheduler  *scheduler.Scheduler  // nil in CLI mode
}

type Options struct {
    WithScheduler bool  // only true for serve command
    NoWebhooks    bool  // disable webhook delivery
}

func New(opts Options) (*Core, error)
func (c *Core) Close()
```

Key design: `New()` accepts options. In CLI mode, `WithScheduler: false` skips Scheduler creation entirely. The Scheduler's `New()` calls `s.cron.Start()` which starts background goroutines that must NOT run in CLI mode.

When `NoWebhooks` is true, `hooks.Manager` is replaced with a no-op implementation (or simply not initialized). This allows `--no-webhooks` to work cleanly.

### Synchronous sync for CLI mode

The server's `Scheduler.syncProduct()` launches goroutines for auto-downloads (`go s.downloader.Download(...)`). The CLI cannot use this. Instead, extract the sync logic into a reusable function:

```go
// internal/sync/sync.go
func SyncProduct(ctx context.Context, db *database.DB, registry *sources.Registry, hooks *hooks.Manager, productID string) (newFiles int, err error)
```

This is the pure sync logic (fetch deliveries, fetch files, create DB records, emit events) without goroutines or auto-download. Both the Scheduler and the CLI call this. The Scheduler wraps it and additionally triggers auto-downloads.

## CLI Design

```
bulk-file-loader [global flags] <command> [subcommand] [flags]
```

### Global flags

```
--data-dir PATH      Data directory (env: BULK_LOADER_DATA_DIR, default: ./data)
--db-driver DRIVER   Database driver (env: BULK_LOADER_DB_DRIVER, default: sqlite)
--db-dsn DSN         Database connection string (env: BULK_LOADER_DB_DSN)
--timeout SECONDS    Download timeout in seconds (env: BULK_LOADER_DOWNLOAD_TIMEOUT, default: 3600)
--max-concurrent N   Maximum concurrent downloads (env: BULK_LOADER_MAX_CONCURRENT, default: 3)
--format FORMAT      Output format: table, json, csv (default: table)
-q, --quiet          Minimal output, suitable for piping
--no-webhooks        Disable webhook delivery (default: webhooks fire in CLI mode too)
```

Precedence: flag > env var > default.

### Commands

#### Setup

```
bulk-file-loader setup
```

First-time passphrase configuration. Prompts for passphrase interactively (or reads from `BULK_LOADER_PASSPHRASE` env var). Creates passphrase_hash, passphrase_salt, encryption_salt in the Settings table.

If already configured, prints "Already configured" and exits 0.

Any credential-dependent command (`source enable`, `source test`, `pull`) on an unconfigured database prints "Run 'bulk-file-loader setup' first" and exits with code 2.

#### Server

```
bulk-file-loader serve [--port PORT] [--dev]
```

Starts the HTTP server with web UI, scheduler, and webhook delivery. This is the existing behavior. Runs until interrupted. `--dev` enables development mode (non-secure cookies, debug logging, Vite proxy).

Note: The embedded frontend (`//go:embed web/ui/dist/*`) adds ~500KB to the binary. Acceptable. A build tag can exclude it later for CLI-only builds if needed.

#### Status

```
bulk-file-loader status
```

Shows summary from database: total files, downloaded, pending, failed, enabled sources. Computed directly from DB queries (same logic as `GET /stats` handler).

Exit code 0 always (informational).

#### Sources

```
bulk-file-loader source ls
bulk-file-loader source show <id>
bulk-file-loader source enable <id> [--username USER --password PASS | --api-key KEY]
bulk-file-loader source disable <id>
bulk-file-loader source test <id>
```

Maps to:
- `ls` -> lists sources with: id, name, enabled, hasCredentials, lastSyncAt
- `show` -> source detail with credential field definitions
- `enable` -> sets enabled=true, encrypts and stores credentials. If credentials not provided via flags, prompts interactively. Also triggers synchronous product sync (same as web UI).
- `disable` -> sets enabled=false
- `test` -> validates credentials against remote API without enabling

Requires passphrase for `enable` and `test` (credential encryption/decryption). Exits with code 2 if unconfigured.

#### Products

```
bulk-file-loader product ls [--source ID]
bulk-file-loader product show <id>
bulk-file-loader product sync <id>
bulk-file-loader product sync --all
bulk-file-loader product enable <id> [--schedule "0 6 * * *"]
bulk-file-loader product disable <id>
```

Maps to:
- `ls` -> list products with: id, sourceId, name, autoDownload, schedule, totalFiles, downloadedFiles, failedFiles (schedule info folded into product listing)
- `show` -> product detail with deliveries
- `sync` -> runs synchronous sync (uses extracted SyncProduct function, not Scheduler). Prints count of new files found.
- `enable` -> sets autoDownload=true. If `--schedule` provided, sets checkWindowStart; if omitted, preserves existing schedule or uses the product's default. Only effective in serve mode; prints note about this.
- `disable` -> sets autoDownload=false, cancels active downloads for product

#### Files

```
bulk-file-loader file ls [--product ID] [--source ID] [--status STATUS] [--limit N] [--offset N]
bulk-file-loader file show <id>
bulk-file-loader file download <id>...
bulk-file-loader file cancel <id>...
bulk-file-loader file skip <id>...
bulk-file-loader file unskip <id>...
bulk-file-loader file reset <id>...
bulk-file-loader file delete <id>...
```

Maps to:
- `ls` -> list files with: id, fileName, fileSize, releasedAt, status, errorMessage
  - Status enum for filter: available, downloading, downloaded, failed, skipped, deleted (derived from latest DownloadEntry; `cancelled` is a DownloadEntry status, not a file-level filter in the API)
- `show` -> file detail with download history (DownloadEntry records: id, status, progress, totalBytes, localPath, localChecksum, errorMessage, startedAt, completedAt)
- `download` -> blocks with progress bar per file, sequential. Checks DB for `status = 'downloading'` to avoid conflict with running server. Accepts multiple IDs. Exit code 3 if some failed.
- `cancel` -> cancels in-progress download (only works if download was started by this CLI process; server downloads have separate active map)
- `skip/unskip/reset/delete` -> immediate DB operations

#### Downloads

```
bulk-file-loader download history [--status STATUS] [--limit N] [--offset N]
bulk-file-loader download all [--product ID] [--source ID]
```

Maps to:
- `history` -> list DownloadEntry records from DB (completed/failed/cancelled, not live progress). Shows: fileId, fileName, status, startedAt, completedAt, errorMessage. Named "history" to avoid confusion with "active downloads" concept.
- `all` -> downloads all files with status=available. Sequential with progress. Exit code 3 if some failed.

#### Pull (convenience)

```
bulk-file-loader pull [--source ID] [--product ID]
```

Sync all enabled sources (or filtered), then download all available files. Shows progress. Exits when done.

Equivalent to:
```
bulk-file-loader product sync --all
bulk-file-loader download all
```

Webhook events fire during pull (sync.completed, download.started, download.completed, etc.) unless `--no-webhooks` is set.

Exit code: 0 if all succeeded, 3 if some downloads failed, 1 if sync failed entirely.

#### Webhooks

```
bulk-file-loader webhook ls
bulk-file-loader webhook add <name> <url> [--events EVENT,EVENT]
bulk-file-loader webhook update <id> [--name NAME] [--url URL] [--events EVENT,EVENT] [--enabled BOOL]
bulk-file-loader webhook rm <id>
```

Events: file.available, download.started, download.completed, download.failed, download.cancelled, checksum.mismatch, sync.completed, sync.failed, or `*` for all.

Webhook delivery is active in CLI mode by default (useful for cron-based pull with external monitoring). Disable with `--no-webhooks` global flag.

#### Version

```
bulk-file-loader version
```

Shows version (set via ldflags at build time).

### Output formats

**Table** (default, human-readable):
```
$ bulk-file-loader source ls
ID                  NAME              STATUS    CREDENTIALS   LAST SYNC
dpma-connect-plus   DPMA Connect Plus enabled   configured    2026-04-02
epo-bdds            EPO BDDS          enabled   configured    2026-04-02
uspto-odp           USPTO ODP         disabled  none          -

$ bulk-file-loader product ls --source epo-bdds
ID                    NAME                           AUTO     SCHEDULE       FILES
epo-bdds:10           14.1 EP bibliographic data     manual   -              85/88 (1 failed)
epo-bdds:3            14.7 DOCDB front file          enabled  0 6 * * *      0/305

$ bulk-file-loader file ls --product epo-bdds:10 --status failed
ID                              FILE NAME         SIZE      RELEASED     STATUS  ERROR
epo-bdds:10:2791:7924           EBD_2540.zip      16 MB     2025-10-01   failed  Download failed: context...
```

**JSON** (machine-readable, matches OpenAPI schema):
```
$ bulk-file-loader source ls --format json
[{"id":"dpma-connect-plus","name":"DPMA Connect Plus","enabled":true,"hasCredentials":true,...}]
```

**CSV**:
```
$ bulk-file-loader source ls --format csv
id,name,enabled,hasCredentials,lastSyncAt
dpma-connect-plus,DPMA Connect Plus,true,true,2026-04-02T16:43:22Z
```

**Quiet** (IDs only, for piping):
```
$ bulk-file-loader file ls -q --status failed | xargs bulk-file-loader file download
```

### Progress display

For `file download`, `download all`, and `pull`:

```
$ bulk-file-loader pull
Syncing EPO BDDS... 15 products, 88 new files
Syncing DPMA Connect Plus... 3 products, 0 new files

Downloading 12 files:
[1/12] EBD_2610.zip                48.4 MB / 48.4 MB  100%  12s (done)     4.0 MB/s
[2/12] dpma-patent-disclosure...  178.0 MB downloaded   42s               4.2 MB/s
```

Sequential downloads with single-line `\r` progress overwrite.

When `--quiet`, only prints completed filenames (one per line).
When `--format json`, outputs one JSON object per completed download.

## Auth in direct mode

The CLI needs the passphrase to decrypt stored credentials (for `source enable`, `source test`, `product sync`, `file download`, `pull`).

Flow:
1. Check if passphrase is configured (Settings table has passphrase_hash)
2. If not configured: exit with code 2, message "Run 'bulk-file-loader setup' first"
3. If configured: read passphrase from `BULK_LOADER_PASSPHRASE` env var, or prompt interactively
4. Verify against stored hash
5. Derive encryption key from passphrase + encryption_salt
6. Load and decrypt source credentials

Read-only commands that don't need credentials (`source ls`, `product ls`, `file ls`, `status`, `download history`) work without passphrase.

## Concurrency and server coexistence

- CLI opens its own database connection with WAL mode (already enabled)
- CLI and server can run simultaneously on the same data directory
- Downloads started via CLI are visible in the web UI after a file list refresh (status is in the DB)
- Active download progress from CLI is NOT visible in the web UI's SSE stream (separate process, separate in-memory tracker). This is acceptable.
- Duplicate download prevention: CLI checks DB for DownloadEntry with `status = 'downloading'` for the target file before starting. Prints warning and skips if found.
- Direct mode avoids routing through own HTTP server, which removes one layer of timeout/proxy issues in corporate environments with restrictive gateways.

## Database schema coverage

All CLI commands map directly to the existing database models:

| Model | CLI commands | Key fields |
|-------|-------------|-----------|
| Source | `source ls/show/enable/disable/test` | ID, Name, Enabled, CredentialsEnc, LastSyncAt |
| Product | `product ls/show/sync/enable/disable` | ID, SourceID, ExternalID, Name, Description, AutoDownload, CheckWindowStart, CheckWindowEnd, LastCheckedAt |
| Delivery | `product show` (nested) | ID, ProductID, ExternalID, Name, PublishedAt, ExpiresAt |
| File | `file ls/show/download/cancel/skip/unskip/reset/delete` | ID, DeliveryID, ProductID, SourceID, ExternalID, FileName, FileSize, ExpectedChecksum, ChecksumAlgorithm, DownloadURI, ReleasedAt, Skipped |
| DownloadEntry | `file show` (history), `download history` | ID, FileID, Status, Progress, TotalBytes, LocalPath, LocalChecksum, ErrorMessage, StartedAt, CompletedAt |
| Webhook | `webhook ls/add/update/rm` | ID, Name, URL, Events, Headers, Enabled |
| Setting | `setup`, internal auth | Key, Value (passphrase_hash, passphrase_salt, encryption_salt) |

## OpenAPI endpoint coverage

| Endpoint | CLI equivalent |
|----------|---------------|
| `GET /auth/status` | implicit in `setup` (checks if configured) |
| `POST /auth/setup` | `setup` |
| `POST /auth/login` | not needed (direct DB access) |
| `POST /auth/logout` | not needed (no session) |
| `GET /sources` | `source ls` |
| `GET /sources/{id}` | `source show` |
| `PUT /sources/{id}` | `source enable/disable` |
| `POST /sources/{id}/test` | `source test` |
| `GET /products` | `product ls` |
| `GET /products/{id}` | `product show` |
| `POST /products/{id}/sync` | `product sync` |
| `GET /files` | `file ls` |
| `GET /files/{id}` | `file show` |
| `DELETE /files/{id}` | `file delete` |
| `POST /files/{id}/download` | `file download` |
| `POST /files/{id}/cancel` | `file cancel` |
| `PUT /files/{id}/skip` | `file skip` |
| `DELETE /files/{id}/skip` | `file unskip` |
| `POST /files/{id}/reset` | `file reset` |
| `GET /downloads` | `download history` |
| `GET /downloads/active` | not applicable (SSE, server-only) |
| `GET /schedule` | folded into `product ls` output |
| `PUT /schedule/{productId}` | `product enable/disable` |
| `GET /hooks` | `webhook ls` |
| `POST /hooks` | `webhook add` |
| `PUT /hooks/{id}` | `webhook update` |
| `DELETE /hooks/{id}` | `webhook rm` |
| `GET /health` | `status` |
| `GET /stats` | `status` |

## Implementation phases

### Phase 1: Core extraction + serve command
- Extract shared initialization from `main.go` into `internal/core/core.go` with `Options`
- Extract synchronous sync logic from `scheduler.go` into `internal/sync/sync.go`
- Move server logic into `cmd/bulk-file-loader/serve.go`
- Wire up cobra root command with global flags
- `bulk-file-loader serve` works exactly like today
- `bulk-file-loader version`

### Phase 2: Read-only commands + output formatting
- `status`, `source ls/show`, `product ls/show`, `file ls/show`, `download history`
- `output.go` with table/json/csv/quiet formatters
- No side effects, safe to build and test incrementally

### Phase 3: Setup + source management
- `setup` command for first-time passphrase
- `source enable/disable/test`
- Interactive credential prompt with fallback to flags/env
- Exit code 2 for "run setup first"

### Phase 4: Product sync
- `product sync` (uses extracted synchronous sync)
- `product sync --all`
- `product enable/disable`

### Phase 5: File operations + progress
- `file download` with terminal progress bar
- `file cancel/skip/unskip/reset/delete`
- `download all`
- Exit code 3 for partial failures

### Phase 6: Pull
- `pull` combining sync + download all
- The flagship command for on-demand users

### Phase 7: Webhooks + polish
- `webhook ls/add/update/rm`
- Shell completion (bash, zsh, fish)
- Man page generation from cobra
- README quick reference section
- `docs/cli.md` full documentation

## Testing strategy

### Unit tests
- `internal/core/core_test.go` -- New() with various Options, Close() cleanup
- `internal/sync/sync_test.go` -- SyncProduct with mock adapters
- `cmd/bulk-file-loader/output_test.go` -- table/json/csv formatting

### Integration tests
- Each command tested against an in-memory SQLite database
- Mock adapters for source operations
- Test exit codes for success, error, partial failure, auth required
- Test `--format` flag for each output-producing command
- Test `--quiet` mode produces only IDs

### Manual testing
- `serve` still works identically to current behavior
- `pull` end-to-end with real credentials
- Server + CLI coexistence on same data directory

## Dependencies

- `github.com/spf13/cobra` for command parsing
- No other new dependencies needed. Progress bar uses raw terminal escape sequences.

## Example workflows

**Always-on (Docker)**:
```bash
docker run -d -p 8080:8080 -v ./data:/app/data patentdev/bulk-file-loader serve
```

**On-demand, one-shot 500GB download (ETL use case)**:
```bash
export BULK_LOADER_PASSPHRASE=secret
export BULK_LOADER_DATA_DIR=/data/patents
bulk-file-loader setup
bulk-file-loader source enable epo-bdds --username user@example.com --password pass
bulk-file-loader pull --source epo-bdds --timeout 600
```

**Weekly cron job**:
```bash
# /etc/cron.d/patent-download
0 8 * * FRI BULK_LOADER_PASSPHRASE=secret bulk-file-loader pull --data-dir /data/patents 2>&1 | logger -t patent-download
```

**Scripting and pipeline integration**:
```bash
# Retry all failed downloads
bulk-file-loader file ls -q --status failed | xargs bulk-file-loader file download

# Export file manifest as CSV
bulk-file-loader file ls --format csv --source epo-bdds > manifest.csv

# Check status in monitoring
bulk-file-loader status --format json | jq '.pendingFiles'

# Show full audit trail for a file
bulk-file-loader file show epo-bdds:10:2791:7924

# Sync metadata without downloading
bulk-file-loader product sync --all

# Download specific files
bulk-file-loader file download epo-bdds:10:2791:7924 epo-bdds:10:2791:7925
```
