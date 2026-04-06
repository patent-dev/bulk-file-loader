# CLI Reference

## Overview

`bulk-file-loader` downloads bulk patent data files from the EPO (European Patent Office), USPTO (US Patent and Trademark Office), and DPMA (German Patent and Trade Mark Office). It supports two modes:

- **`serve`** - web UI with automatic scheduled downloads (for always-on setups)
- **All other commands** - direct CLI access, no server needed (for scripting and one-shot downloads)

Both modes share the same data directory and database. You can use them interchangeably.

### Data model

- **Sources** are patent offices (EPO BDDS, USPTO ODP, DPMA Connect Plus). Each source requires credentials.
- **Products** are data collections within a source (e.g., "14.1 EP bibliographic data", "Patent Grant Full-Text Data").
- **Files** are the individual downloadable archives within a product release.

### Getting started

```bash
# 1. Set passphrase (protects stored credentials)
export BULK_LOADER_PASSPHRASE=your-secret-passphrase

# 2. Enable a data source with credentials
bulk-file-loader source enable epo-bdds --username user@example.com --password secret

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

Show system statistics: total files, downloaded, pending, active downloads, enabled sources.

### Sources

```bash
bulk-file-loader source ls
bulk-file-loader source show <id>
bulk-file-loader source enable <id> [--username USER --password PASS | --api-key KEY]
bulk-file-loader source disable <id>
bulk-file-loader source test <id> [--username USER --password PASS | --api-key KEY]
```

Available sources: `epo-bdds`, `uspto-odp`, `dpma-connect-plus`.

### Products

```bash
bulk-file-loader product ls [--source SOURCE_ID]
bulk-file-loader product show <id>
bulk-file-loader product sync <id>
bulk-file-loader product sync --all
bulk-file-loader product enable <id> [--schedule "0 6 * * *"]
bulk-file-loader product disable <id>
```

`sync` fetches the latest file list from the remote API. `enable`/`disable` control auto-download scheduling (only effective in `serve` mode).

### Files

```bash
bulk-file-loader file ls [--product ID] [--source ID] [--status STATUS] [--limit N] [--offset N]
bulk-file-loader file show <id>
bulk-file-loader file download <id> [<id>...]
bulk-file-loader file cancel <id> [<id>...]
bulk-file-loader file skip <id> [<id>...]
bulk-file-loader file unskip <id> [<id>...]
bulk-file-loader file reset <id> [<id>...]
bulk-file-loader file delete <id> [<id>...]
```

Status values: `available`, `downloading`, `downloaded`, `failed`, `skipped`, `deleted`.

`download` shows a progress bar on stderr. Multiple IDs can be specified. `reset` clears download history and returns a file to `available` status.

### Downloads

```bash
bulk-file-loader download history [--status STATUS] [--limit N] [--offset N]
bulk-file-loader download all [--product ID] [--source ID]
```

`history` shows past download attempts. `all` downloads every file with `available` status.

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
ID                 Name               Enabled  Has Credentials  Last Sync
-----------------  -----------------  -------  ---------------  ---------
dpma-connect-plus  DPMA Connect Plus  yes      yes              2026-04-02
epo-bdds           EPO BDDS           yes      yes              2026-04-02
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
dpma-connect-plus,DPMA Connect Plus,yes,yes,2026-04-02
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

`pull`, `download all`, and multi-ID commands return exit code 3 when some files fail but others succeed. Scripts can check `$?` to decide whether to retry.

## Examples

### First-time setup

```bash
export BULK_LOADER_PASSPHRASE=my-secret
bulk-file-loader source enable epo-bdds --username user@example.com --password secret
bulk-file-loader source enable dpma-connect-plus --username myuser --password mypass
bulk-file-loader pull
```

### One-shot download of specific products

```bash
export BULK_LOADER_PASSPHRASE=my-secret
bulk-file-loader source enable epo-bdds --username user@example.com --password secret
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
bulk-file-loader file ls -q --status failed | xargs bulk-file-loader file download

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
