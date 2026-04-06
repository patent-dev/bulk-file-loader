# CLI Implementation Phases

## Architecture decision: client mode vs direct mode

### What oapi-codegen gives us for free

The OpenAPI spec can generate not just the server interface + models (current), but also a full HTTP client with typed responses (`ClientWithResponses`). This means:

- **Server mode** (`bulk-file-loader serve`): uses generated server interface (existing)
- **Client mode** (`bulk-file-loader --server URL <cmd>`): uses generated HTTP client, talks to running server
- **Direct mode** (`bulk-file-loader <cmd>`): bypasses HTTP, operates on DB directly

### Chosen approach: direct-first, client optional

The primary mode is **direct** (no server needed). This covers the ETL/scripting use case (Nicolas's 500GB one-shot download). The handlers in `api/handlers/handlers.go` contain business logic (DB queries, file count computation, status derivation) that is currently coupled to HTTP request/response. Rather than duplicating this logic in the CLI, we extract it.

### Code reuse strategy

```
api/generated/server.go    -- generated: ServerInterface, models (types), embedded spec
api/generated/client.go    -- NEW generated: ClientWithResponses (HTTP client)
api/handlers/handlers.go   -- HTTP handlers (thin: parse request, call service, write response)
internal/service/           -- NEW: extracted business logic (shared by handlers AND CLI)
  sources.go               -- ListSources, EnableSource, DisableSource, TestCredentials
  products.go              -- ListProducts, GetProduct, SyncProduct, EnableProduct
  files.go                 -- ListFiles, GetFile, DownloadFile, SkipFile, ResetFile, DeleteFile
  downloads.go             -- ListDownloads, DownloadAll
  webhooks.go              -- ListWebhooks, CreateWebhook, UpdateWebhook, DeleteWebhook
  stats.go                 -- GetStats
```

The service layer holds the real logic. Handlers become thin wrappers:

```go
// Before (handler does everything):
func (h *Handler) ListProducts(w http.ResponseWriter, r *http.Request, params generated.ListProductsParams) {
    var products []database.Product
    query := h.db.DB
    if params.SourceId != nil { query = query.Where(...) }
    // ... 30 lines of DB queries, counting, conversion
    writeJSON(w, http.StatusOK, result)
}

// After (handler delegates to service):
func (h *Handler) ListProducts(w http.ResponseWriter, r *http.Request, params generated.ListProductsParams) {
    result, err := h.svc.ListProducts(params.SourceId)
    if err != nil { writeError(w, 500, err.Error()); return }
    writeJSON(w, http.StatusOK, result)
}
```

The CLI calls the same service directly:

```go
// CLI:
func runProductLs(cmd *cobra.Command, args []string) error {
    products, err := core.Service.ListProducts(sourceID)
    // ... format and print
}
```

### Generated types reuse

Both the HTTP client mode and direct mode use `generated.Product`, `generated.File`, etc. for JSON output. The `--format json` output matches the API responses exactly because it uses the same types.

## Phase breakdown

### Phase 0: Generate HTTP client (trivial)

Add a second oapi-codegen config to generate the client:

```yaml
# api/oapi-client.yaml
package: client
output: api/client/client.go
generate:
  client: true
  models: false  # reuse models from generated package
import-mapping:
  server.go: github.com/patent-dev/bulk-file-loader/api/generated
```

Update CI/Makefile to generate both server and client. Add `api/client/` to `.gitignore`.

This gives us `client.ClientWithResponses` with typed methods like:
- `ListProductsWithResponse(ctx, &ListProductsParams{SourceId: &id})`
- `DownloadFileWithResponse(ctx, fileID)`
- etc.

Not used in Phase 1 but available for a future `--server` flag.

### Phase 1: Extract service layer + core

**Goal**: Move business logic from handlers into `internal/service/`. Handlers become thin. No CLI code yet -- just refactoring.

Files:
- `internal/service/service.go` -- Service struct holding db, registry, downloader, hooks, auth
- `internal/service/sources.go` -- ListSources, GetSource, EnableSource, DisableSource, TestCredentials
- `internal/service/products.go` -- ListProducts, GetProduct, SyncProduct, EnableAutoDownload, DisableAutoDownload
- `internal/service/files.go` -- ListFiles, GetFile, DownloadFile, CancelDownload, SkipFile, UnskipFile, ResetFile, DeleteFile
- `internal/service/downloads.go` -- ListDownloads
- `internal/service/webhooks.go` -- ListWebhooks, CreateWebhook, UpdateWebhook, DeleteWebhook
- `internal/service/stats.go` -- GetStats
- `internal/service/sync.go` -- SyncProduct (extracted from scheduler, synchronous, no goroutines)

The service methods return `generated.*` types (or error), so they can be used by both handlers and CLI without conversion.

Handlers are updated to call service methods. All existing tests must still pass.

**Validation**: `go test ./...` passes, `serve` works identically.

### Phase 2: Core initialization + cobra skeleton

Files:
- `internal/core/core.go` -- Core struct, New(Options), Close()
- `cmd/bulk-file-loader/main.go` -- cobra root command, global flags
- `cmd/bulk-file-loader/serve.go` -- serve subcommand (moves current main.go logic)
- `cmd/bulk-file-loader/version.go` -- version subcommand

Update existing `main.go` to just call `cmd/bulk-file-loader`.
Update Dockerfile ENTRYPOINT if binary path changes.

**Validation**: `bulk-file-loader serve` works exactly like today. `bulk-file-loader version` prints version.

### Phase 3: Output formatting + read-only commands

Files:
- `cmd/bulk-file-loader/output.go` -- table/json/csv/quiet formatters
- `cmd/bulk-file-loader/status.go`
- `cmd/bulk-file-loader/source.go` -- ls, show
- `cmd/bulk-file-loader/product.go` -- ls, show
- `cmd/bulk-file-loader/file.go` -- ls, show
- `cmd/bulk-file-loader/download.go` -- history

All read-only. Call service layer directly. Test all 4 output formats.

**Validation**: all read-only commands work against existing test data.

### Phase 4: Setup + source management

Files:
- `cmd/bulk-file-loader/setup.go`
- `cmd/bulk-file-loader/source.go` -- add enable, disable, test

Auth flow: setup creates passphrase, enable/test decrypt credentials.

**Validation**: `setup`, `source enable`, `source test` work end-to-end.

### Phase 5: Product sync + management

Files:
- `cmd/bulk-file-loader/product.go` -- add sync, enable, disable

Uses extracted synchronous SyncProduct from service layer.

**Validation**: `product sync --all` discovers new files.

### Phase 6: File operations + progress

Files:
- `cmd/bulk-file-loader/file.go` -- add download, cancel, skip, unskip, reset, delete
- `cmd/bulk-file-loader/download.go` -- add all
- `cmd/bulk-file-loader/progress.go` -- terminal progress bar

The download progress uses `\r` line overwrite. Exit code 3 for partial failures.

**Validation**: `file download` works with progress. `download all` handles multiple files.

### Phase 7: Pull

Files:
- `cmd/bulk-file-loader/pull.go`

Combines sync + download all. The flagship command.

**Validation**: `pull` end-to-end.

### Phase 8: Webhooks + polish + documentation

Files:
- `cmd/bulk-file-loader/webhook.go`
- `docs/cli.md` -- full CLI documentation
- `README.md` -- quick reference section added

Shell completion, man pages via cobra.

**Validation**: full test suite, lint, vet. All commands documented.

## Key insight: service layer is the biggest win

The service extraction (Phase 1) is the most important phase. It:
- Makes handlers testable without HTTP
- Makes CLI possible without duplicating logic
- Makes a future HTTP client mode trivial (just wire client responses to the same output formatters)
- Improves the codebase even without the CLI

The CLI commands themselves are thin: parse flags, call service, format output. Most of the code is in output formatting and progress display, not business logic.

## Dependency on existing code

| Existing code | How CLI reuses it |
|---------------|-------------------|
| `api/generated/*.go` (models) | JSON output types for `--format json` |
| `internal/database/*.go` | Direct DB access in service layer |
| `internal/downloader/*.go` | File download with progress |
| `internal/sources/*.go` | Adapter registry, credential management |
| `internal/auth/*.go` | Passphrase verification, credential encryption |
| `internal/hooks/*.go` | Webhook delivery (optional in CLI) |
| `config/config.go` | Shared configuration |

## What NOT to reuse

| Code | Why not |
|------|---------|
| `internal/scheduler/*.go` | Starts cron goroutines; CLI uses extracted sync function instead |
| `api/handlers/*.go` | HTTP-coupled; replaced by service layer calls |
| `api/client/client.go` | Generated HTTP client; available but not used in direct mode |
