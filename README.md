# Bulk File Loader

- Source: [github.com/patent-dev/bulk-file-loader](https://github.com/patent-dev/bulk-file-loader)
- Docker: [hub.docker.com/r/patentdev/bulk-file-loader](https://hub.docker.com/r/patentdev/bulk-file-loader)

Automated bulk data download manager for patent data from EPO, USPTO, and DPMA.

![Screenshot](screenshots/overview.png)

## Features

- Download bulk patent data from EPO BDDS, USPTO ODP, and DPMA Connect Plus
- CLI for scripting, cron jobs, and data pipeline integration
- Web UI for configuration and monitoring
- Automatic scheduled downloads with retry
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

# Enable a data source
bulk-file-loader source enable epo-bdds --username user@example.com --password secret

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
bulk-file-loader file ls -q --status failed | xargs \
  bulk-file-loader file download                          # retry failed
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
| `BULK_LOADER_DB_DSN` | - | Database connection string (required for postgres/mysql) |
| `BULK_LOADER_DOWNLOAD_TIMEOUT` | 3600 | Per-file download timeout in seconds |
| `BULK_LOADER_MAX_CONCURRENT` | 3 | Maximum concurrent downloads |
| `BULK_LOADER_DEV_MODE` | false | Debug logging, verbose SQL, non-secure cookies, Vite proxy (do not use in production) |
| `BULK_LOADER_TRUSTED_PROXIES` | - | Comma-separated IPs/CIDRs whose `X-Forwarded-Proto` is honored when setting the cookie `Secure` flag |
| `BULK_LOADER_INSECURE_COOKIE` | false | Drop the `Secure` cookie flag. For HTTP-only LAN use; never on the public internet |

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
