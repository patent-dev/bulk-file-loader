# CLI Reference

## Overview

`bulk-file-loader` downloads bulk patent data files from the EPO (European Patent Office), USPTO (US Patent and Trademark Office), and DPMA (German Patent and Trade Mark Office), plus public reference data: patent classifications (CPC, IPC, Nice, Locarno, JPO F-terms) and examination manuals and guidelines (USPTO, EPO, EUIPO, WIPO, IP Australia). It supports two modes:

- **`serve`** - web UI with automatic scheduled downloads (for always-on setups)
- **All other commands** - direct CLI access, no server needed (for scripting and one-shot downloads)

Both modes share the same data directory and database. You can use them interchangeably.

### Data model

- **Sources** are patent offices (EPO BDDS, USPTO ODP, DPMA Connect Plus), which require credentials, and public reference data (Patent Classifications, Examination Manuals & Guidelines), which needs none.
- **Products** are data collections within a source (e.g., "14.1 EP bibliographic data", "Patent Grant Full-Text Data").
- **Files** are the individual downloadable archives within a product release.

### Getting started

```bash
# 1. Set passphrase (protects stored credentials)
export BULK_LOADER_PASSPHRASE=your-secret-passphrase

# 2. Enable a data source (prompts for the password, or reads EPO_BDDS_PASSWORD)
bulk-file-loader source enable epo-bdds --username user@example.com

# 3. Sync metadata and download all available files
bulk-file-loader pull
```

That's it. Files are downloaded to `./data/downloads/` by default.

## Global Flags

```
--data-dir string      Data directory (env: BULK_LOADER_DATA_DIR, default: ./data)
--db-driver string     Database driver (env: BULK_LOADER_DB_DRIVER, default: sqlite)
--db-dsn string        Database connection string (env: BULK_LOADER_DB_DSN)
--timeout int          Download timeout in seconds (env: BULK_LOADER_DOWNLOAD_TIMEOUT, default: 3600)
--max-concurrent int   Max concurrent downloads (env: BULK_LOADER_MAX_CONCURRENT, default: 3)
--format string        Output format: table, json, csv (default: table)
-q, --quiet            Only output IDs, one per line (for piping)
--no-webhooks          Disable webhook delivery
```

Flag values take precedence over environment variables.

## Commands

### Setup

```bash
bulk-file-loader setup
```

Configure the encryption passphrase used to protect source credentials. Prompts interactively with hidden input and confirmation.

When `BULK_LOADER_PASSPHRASE` is set as an environment variable, the passphrase is configured automatically on first use of any command. This is the recommended approach for Docker, cron jobs, and CI/CD.

### Server

```bash
bulk-file-loader serve [--port PORT] [--dev]
```

Start the HTTP server with web UI, scheduler, and webhook delivery. Runs until interrupted.

### Status

```bash
bulk-file-loader status
```

Show the system state: whether a server is running (its process, version and uptime), the database, files downloaded and pending, the download queue (queued, running, retrying, failed) and the transfers in progress in any process. `status -q` prints only the health and exits 1 when unhealthy.

### Sources

```bash
bulk-file-loader source ls
bulk-file-loader source show <id>
bulk-file-loader source enable <id> [--username USER] [--password-stdin | --api-key-stdin] [--sync]
bulk-file-loader source disable <id>
bulk-file-loader source test <id> [--username USER] [--password-stdin | --api-key-stdin]
```

Available sources: `epo-bdds`, `uspto-odp`, `dpma-connect-plus` (credentials required), `patent-classifications`, `examination-manuals` (public, no credentials).

`enable` validates the credentials with one request, stores them encrypted, refreshes the product list and returns. The file catalogue can take minutes for large sources: add `--sync` to fetch it right away (one progress line per product on stderr), or run `product sync --all` or `pull` later. A running server syncs it in the background.

Credentials are read, in this order, from the flags, from stdin (`--password-stdin`, `--api-key-stdin`), from the environment variables the client libraries use, and finally from a hidden prompt when stdin is a terminal:

| Source | Environment variables |
|--------|-----------------------|
| `epo-bdds` | `EPO_BDDS_USERNAME`, `EPO_BDDS_PASSWORD` |
| `dpma-connect-plus` | `DPMA_CONNECT_PLUS_USERNAME`, `DPMA_CONNECT_PLUS_PASSWORD` |
| `uspto-odp` | `USPTO_API_KEY` |

`--password` and `--api-key` still work but are deprecated: their values show up in the process list and shell history.

```bash
printf '%s\n' "$EPO_PASSWORD" | bulk-file-loader source enable epo-bdds --username user@example.com --password-stdin
```

Credentials given this way replace the stored ones on `enable`; with none given, `enable` keeps the stored credentials. `test` tests the given credentials without storing them, or, with none given, the stored ones (needs `BULK_LOADER_PASSPHRASE` to decrypt them).

### Products

```bash
bulk-file-loader product ls [--source SOURCE_ID]
bulk-file-loader product show <id>
bulk-file-loader product sync <id>
bulk-file-loader product sync --all
bulk-file-loader product enable <id> [--schedule "0 6 * * *"]
bulk-file-loader product disable <id>
```

`sync` fetches the latest file list from the remote API and prints one line per product on stderr; a delivery whose file list cannot be fetched is reported and makes the command exit with code 3. `enable`/`disable` control auto-download; the schedule is a five-field cron expression or a descriptor such as `@daily`, validated before it is stored. `enable` prints the stored schedule and the next run. Scheduled syncs run in `serve` mode; a running server picks up a schedule changed with the CLI within 30 seconds. Enabling auto-download queues the product's pending files. `show` (and `ls --format json`) report the schedule, next run and last run.

### Files

```bash
bulk-file-loader file ls [FILTERS] [--limit N] [--offset N]
bulk-file-loader file show <id>
bulk-file-loader file download <id>... | FILTERS [--dry-run]
bulk-file-loader file cancel <id> [<id>...]
bulk-file-loader file skip <id>... | FILTERS [--dry-run]
bulk-file-loader file unskip <id>... | FILTERS [--dry-run]
bulk-file-loader file reset <id> [<id>...]
bulk-file-loader file delete <id> [<id>...]
```

Status values: `available`, `queued`, `downloading`, `retrying`, `downloaded`, `failed`, `cancelled`, `skipped`, `deleted`. `queued` and `retrying` files wait in the download queue; `failed` means given up after the last attempt (`BULK_LOADER_MAX_ATTEMPTS`, default 5), and `file ls` shows the attempts and the last error. `cancel` removes a queued or retrying file from the queue and stops a running download, also one running in another process (the server stops it within 30 seconds); `skip` does the same and marks the file skipped. `reset` refuses a file that is downloading.

Filters select files by attribute instead of ID (IDs and filters cannot be combined):

| Filter | Meaning |
|--------|---------|
| `--product ID`, `--source ID` | files of a product or source |
| `--status STATUS` | files in that status |
| `--released-before DATE` | released before DATE (`YYYY-MM-DD` or RFC 3339) |
| `--released-after DATE` | released on or after DATE |
| `--name-glob PATTERN` | file name matches the pattern (`*`, `?`, `[...]`; case-sensitive) |

`--dry-run` lists the matching files without acting on them. `file download` with filters and no `--status` leaves out files that are downloaded, downloading or skipped.

```bash
# Skip the front-file packages the 2026 back file already covers
bulk-file-loader file skip --product epo-bdds:3 --released-before 2026-08-31 --dry-run
bulk-file-loader file skip --product epo-bdds:3 --released-before 2026-08-31
```

`file ls` shows the newest 50 files by default; `--limit 0` lists all, and the summary on stderr says how many of how many are shown. `reset` clears download history and returns a file to `available` status.

Downloads show a progress line on stderr. On a terminal it is redrawn in place; in a log file or pipe a line is printed at most every 30 seconds, and every file ends with a line giving size, duration and rate.

### Downloads

```bash
bulk-file-loader download history [--status STATUS] [--limit N] [--offset N]
bulk-file-loader download all [--product ID] [--source ID]
```

`history` shows past download attempts. `all` downloads every file that is `available`, `queued` or `retrying`, so it also picks up files left in the queue by an interrupted command.

All download commands (`download all`, `file download`, `pull`) go through the download queue (see [download-queue.md](download-queue.md)):

- with a running server (`serve`), they queue the files and return; the server downloads them, `status` shows the progress, and `--wait` follows until they are done (if the server stops meanwhile, the command takes over and downloads the rest itself);
- without a server, they download the files themselves, `--max-concurrent` at a time, retrying transient failures (1, 2, 4 ... minutes apart) up to `BULK_LOADER_MAX_ATTEMPTS` attempts.

The exit code reflects the end state: 0 when every file was downloaded, 3 when some were given up, 1 when all were. Interrupting a command (Ctrl-C) leaves its unfinished files queued for a server or the next download command.

### Pull

```bash
bulk-file-loader pull [--source ID] [--product ID]
```

Sync enabled sources then download all available files. The single command for one-shot bulk downloads.

When `--product` is specified, only the source owning that product is synced.

### Webhooks

```bash
bulk-file-loader webhook ls
bulk-file-loader webhook add <name> <url> [--events EVENT,EVENT]
bulk-file-loader webhook update <id> [--name NAME] [--url URL] [--events EVENTS] [--enabled BOOL]
bulk-file-loader webhook rm <id>
```

Available events: `file.available`, `download.started`, `download.completed`, `download.failed`, `download.cancelled`, `checksum.mismatch`, `sync.completed`, `sync.failed`, or `*` for all.

Webhooks fire in CLI mode by default. Use `--no-webhooks` to disable.

### Version

```bash
bulk-file-loader version
```

## Output Formats

All commands that produce output support `--format table|json|csv` and `-q` (quiet).

**Table** (default, human-readable):
```
$ bulk-file-loader source ls
ID                      Name                               Enabled  Has Credentials  Last Sync
dpma-connect-plus       DPMA Connect Plus                  yes      yes              2026-11-08
epo-bdds                EPO BDDS                           yes      yes              2026-11-08
examination-manuals     Examination Manuals & Guidelines   no       not needed
patent-classifications  Patent Classifications (CPC, IPC)  yes      not needed       2026-11-08
uspto-odp               USPTO ODP                          no       no
```

**JSON** (matches the API response schema):
```
$ bulk-file-loader source ls --format json
[{"id":"dpma-connect-plus","name":"DPMA Connect Plus","enabled":true,...}]
```

**CSV**:
```
$ bulk-file-loader source ls --format csv
ID,Name,Enabled,Has Credentials,Last Sync
dpma-connect-plus,DPMA Connect Plus,yes,yes,2026-11-08
```

**Quiet** (IDs only, for piping):
```
$ bulk-file-loader file ls -q --status failed
epo-bdds:10:2791:7924
```

Informational messages go to stderr, data to stdout. This means `--format csv` and `-q` output is clean for piping even without `2>/dev/null`.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Error |
| 3 | Partial failure (some operations succeeded, some failed) |

`pull`, `download all`, and multi-ID commands return exit code 3 when some files fail but others succeed. `product sync`, `pull` and `source enable --sync` also return 3 when the catalogue of some products could not be fetched completely. Scripts can check `$?` to decide whether to retry.

## Examples

### First-time setup

```bash
export BULK_LOADER_PASSPHRASE=my-secret
export EPO_BDDS_USERNAME=user@example.com EPO_BDDS_PASSWORD=secret
export DPMA_CONNECT_PLUS_USERNAME=myuser DPMA_CONNECT_PLUS_PASSWORD=mypass
bulk-file-loader source enable epo-bdds
bulk-file-loader source enable dpma-connect-plus
bulk-file-loader pull
```

### One-shot download of specific products

```bash
export BULK_LOADER_PASSPHRASE=my-secret
bulk-file-loader source enable epo-bdds --username user@example.com   # prompts for the password
bulk-file-loader product sync --all
bulk-file-loader file ls --source epo-bdds --status available --format csv > manifest.csv
bulk-file-loader download all --source epo-bdds
```

### Weekly cron job

```bash
# /etc/cron.d/patent-download
BULK_LOADER_PASSPHRASE=secret
BULK_LOADER_DATA_DIR=/data/patents
0 8 * * FRI bulk-file-loader pull 2>&1 | logger -t patent-download
```

### Pipeline integration

```bash
# Retry all failed downloads
bulk-file-loader file download --status failed

# Export available files as CSV for external processing
bulk-file-loader file ls --format csv --source epo-bdds --status available > todo.csv

# Check status in monitoring
bulk-file-loader status --format json | jq '.stats.pendingFiles'

# Download specific files by ID
bulk-file-loader file download epo-bdds:20:470:885 epo-bdds:20:470:886
```

### Docker with web UI

```bash
docker run -d -p 8080:8080 -v ./data:/app/data patentdev/bulk-file-loader serve
```

### Run both web UI and CLI on the same data

```bash
# Terminal 1: start server
BULK_LOADER_DATA_DIR=/data/patents bulk-file-loader serve

# Terminal 2: use CLI against same data
BULK_LOADER_DATA_DIR=/data/patents bulk-file-loader status
BULK_LOADER_DATA_DIR=/data/patents bulk-file-loader file ls --status failed
```
