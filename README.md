# Bulk File Loader

- Source: [github.com/patent-dev/bulk-file-loader](https://github.com/patent-dev/bulk-file-loader)
- Docker: [hub.docker.com/r/patentdev/bulk-file-loader](https://hub.docker.com/r/patentdev/bulk-file-loader)

Automated bulk data download manager for patent data from EPO, USPTO, and DPMA, and for public patent reference data (classifications, examination manuals).

![Screenshot](screenshots/overview.png)

## Features

- Download bulk patent data from EPO BDDS, USPTO ODP, and DPMA Connect Plus
- Download public reference data without credentials: CPC, IPC, Nice, Locarno and JPO F-term classifications; USPTO MPEP and TMEP, EPO, EUIPO, WIPO PCT and IP Australia examination guidelines
- CLI for scripting, cron jobs, and data pipeline integration
- Web UI for configuration and monitoring
- Automatic scheduled downloads through a queue the server owns, with retries and backoff
- Webhook notifications
- Multi-database support (SQLite, PostgreSQL, MySQL)

## Quick Start

### Web UI

```bash
docker run -p 8080:8080 -v ./data:/app/data patentdev/bulk-file-loader serve
```

Open http://localhost:8080 and set your passphrase.

### CLI

```bash
# Set passphrase (protects stored credentials)
export BULK_LOADER_PASSPHRASE=my-secret

# Enable a data source (prompts for the password, or reads EPO_BDDS_PASSWORD)
bulk-file-loader source enable epo-bdds --username user@example.com

# Sync and download everything
bulk-file-loader pull
```

Files are downloaded to `./data/downloads/`. See [docs/cli.md](docs/cli.md) for the full CLI reference.

### Common CLI commands

```bash
bulk-file-loader source ls                               # list sources
bulk-file-loader product ls --source epo-bdds            # list products
bulk-file-loader file ls --status available               # list available files
bulk-file-loader download all --source epo-bdds           # download all available
bulk-file-loader file download --status failed           # retry failed
bulk-file-loader file skip --product epo-bdds:3 \
  --released-before 2026-08-31 --dry-run                   # preview a bulk skip
bulk-file-loader status --format json                     # machine-readable status
```

## Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `BULK_LOADER_PASSPHRASE` | - | Passphrase for credential encryption (auto-configures on first use) |
| `BULK_LOADER_PORT` | 8080 | HTTP port |
| `BULK_LOADER_DATA_DIR` | ./data | Data directory |
| `BULK_LOADER_DB_DRIVER` | sqlite | Database driver (sqlite, postgres, mysql) |
| `BULK_LOADER_DB_DSN` | - | Database connection string (required for postgres/mysql), e.g. `postgres://user:pass@host:5432/db` or `user:pass@tcp(host:3306)/db` |
| `BULK_LOADER_DOWNLOAD_TIMEOUT` | 3600 | Per-file download timeout in seconds |
| `BULK_LOADER_MAX_CONCURRENT` | 3 | Maximum concurrent downloads, in the server and in CLI commands |
| `BULK_LOADER_MAX_ATTEMPTS` | 5 | Download attempts per file before it is given up; transient failures are retried after 1, 2, 4 ... minutes (see [docs/download-queue.md](docs/download-queue.md)) |
| `BULK_LOADER_DEV_MODE` | false | Debug logging, verbose SQL, non-secure cookies, Vite proxy (do not use in production) |
| `BULK_LOADER_TRUSTED_PROXIES` | - | Comma-separated IPs/CIDRs whose `X-Forwarded-Proto` is honored when setting the cookie `Secure` flag |
| `BULK_LOADER_INSECURE_COOKIE` | false | Drop the `Secure` cookie flag. For HTTP-only LAN use; never on the public internet |

### Source credentials

Credentials are entered once, in the web UI or with `bulk-file-loader source enable`, and stored encrypted with the passphrase. The CLI also reads them from the environment variables the client libraries use: `EPO_BDDS_USERNAME` and `EPO_BDDS_PASSWORD`, `DPMA_CONNECT_PLUS_USERNAME` and `DPMA_CONNECT_PLUS_PASSWORD`, `USPTO_API_KEY` (see [docs/cli.md](docs/cli.md#sources)).

The EPO BDDS source logs in with your EPO username and password directly. It is not affected by two-factor authentication on the EPO website: an account with 2FA enabled works unchanged.

### Docker

The image runs as `appuser` with the fixed ids uid 100 and gid 101. A bind-mounted data directory must be writable by that user:

```
mkdir -p ./data && sudo chown -R 100:101 ./data
docker run -p 8080:8080 -v ./data:/app/data patentdev/bulk-file-loader
```

`GET /healthz` (also `/api/health`) is public and reports the database, the scheduler and the active downloads; it returns 503 when the database is unreachable. The image's `HEALTHCHECK` uses it, so `docker ps` shows the container as healthy or unhealthy. Change the port with `BULK_LOADER_PORT` rather than `serve --port`, so the health check uses the same port.

### Running the tests against PostgreSQL or MySQL

`go test ./...` uses SQLite by default. Point the suite at a server to run every database test against it; each test creates and drops its own database, so the user needs `CREATE DATABASE` rights:

```
BULK_LOADER_TEST_DB_DRIVER=mysql BULK_LOADER_TEST_DB_DSN='root:pass@tcp(127.0.0.1:3306)/' go test ./...
BULK_LOADER_TEST_DB_DRIVER=postgres BULK_LOADER_TEST_DB_DSN='postgres://postgres:pass@127.0.0.1:5432/postgres?sslmode=disable' go test ./...
```

The reference-data tests replay HTTP exchanges recorded from the real sources (`internal/sources/refdata/testdata`). Re-record them with `REFDATA_CAPTURE=1 go test ./internal/sources/refdata/ -run TestCapture`, or run the live checks with `REFDATA_LIVE=1 go test ./internal/sources/refdata/ -run Live`.

### Behind a reverse proxy

Set `BULK_LOADER_TRUSTED_PROXIES` to the IPs or CIDRs the proxy connects from; the cookie `Secure` flag then follows `X-Forwarded-Proto` from those peers and ignores it from anyone else.

```
docker run -p 8080:8080 \
  -e BULK_LOADER_TRUSTED_PROXIES=10.0.0.0/8,127.0.0.1 \
  -v ./data:/app/data patentdev/bulk-file-loader serve
```

### HTTP-only LAN

For a LAN-only deployment without TLS, set `BULK_LOADER_INSECURE_COOKIE=true` so browsers accept the session cookie over plain HTTP.

## Related Projects

Part of the [patent.dev](https://patent.dev) open-source patent data ecosystem:

- [uspto-odp](https://github.com/patent-dev/uspto-odp) - USPTO Open Data Portal client (search, PTAB, XML full text)
- [epo-ops](https://github.com/patent-dev/epo-ops) - EPO Open Patent Services client (search, biblio, legal status, family, images)
- [epo-bdds](https://github.com/patent-dev/epo-bdds) - EPO Bulk Data Distribution Service client
- [dpma-connect-plus](https://github.com/patent-dev/dpma-connect-plus) - DPMA Connect Plus client (patents, designs, trademarks)

## License

MIT - See [LICENSE](LICENSE)
