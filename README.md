# Bulk File Loader

Automated bulk data download manager for patent data.

![Screenshot](screenshots/overview.png)

## Features

- Automated scheduled downloads from EPO, USPTO and DPMA
- Web UI for configuration and monitoring
- Webhook notifications
- Multi-database support (SQLite, PostgreSQL, MySQL)

## Quick Start

```bash
# Using Docker
docker run -p 8080:8080 -v ./data:/app/data patentdev/bulk-file-loader

# Or download binary from releases
./bulk-file-loader
```

Open http://localhost:8080 and set your passphrase.

## Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `BULK_LOADER_PASSPHRASE` | - | Required for auth |
| `BULK_LOADER_PORT` | 8080 | HTTP port |
| `BULK_LOADER_DATA_DIR` | ./data | Data directory |
| `BULK_LOADER_DB_DRIVER` | sqlite | Database driver (sqlite, postgres, mysql) |
| `BULK_LOADER_DB_DSN` | - | Database connection string (required for postgres/mysql) |
| `BULK_LOADER_DOWNLOAD_TIMEOUT` | 3600 | Per-file download timeout in seconds |
| `BULK_LOADER_MAX_CONCURRENT` | 3 | Maximum concurrent downloads |
| `BULK_LOADER_DEV_MODE` | false | Enable development mode |

## Related Projects

Part of the [patent.dev](https://patent.dev) open-source patent data ecosystem:

- [uspto-odp](https://github.com/patent-dev/uspto-odp) - USPTO Open Data Portal client (search, PTAB, XML full text)
- [epo-ops](https://github.com/patent-dev/epo-ops) - EPO Open Patent Services client (search, biblio, legal status, family, images)
- [epo-bdds](https://github.com/patent-dev/epo-bdds) - EPO Bulk Data Distribution Service client
- [dpma-connect-plus](https://github.com/patent-dev/dpma-connect-plus) - DPMA Connect Plus client (patents, designs, trademarks)

## License

MIT - See [LICENSE](LICENSE)

---

Built by [patent.dev](https://patent.dev) - Wolfgang Stark
