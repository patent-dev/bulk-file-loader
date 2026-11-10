# Build frontend
FROM node:26-alpine AS frontend-builder

WORKDIR /app/web/ui
COPY web/ui/package*.json ./
RUN npm ci
COPY web/ui/ ./
RUN npm run build

# Build backend
FROM golang:1.27-alpine AS backend-builder

RUN apk add --no-cache git

WORKDIR /app

# Install oapi-codegen
RUN go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.2

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Generate API code
RUN oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml && \
    mkdir -p api/client && \
    oapi-codegen -config api/oapi-client.yaml api/openapi.yaml

# Copy built frontend
COPY --from=frontend-builder /app/web/ui/dist ./web/ui/dist

ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X github.com/patent-dev/bulk-file-loader/cmd/bulk-file-loader.Version=${VERSION}" -o bulk-file-loader .

# Runtime image
FROM alpine:3.24

# Fixed ids (uid 100, gid 101, as in earlier images) so bind mounts and
# volumes can be owned correctly: chown -R 100:101 <data dir>.
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S -g 101 appgroup && adduser -S -u 100 -G appgroup appuser

WORKDIR /app

COPY --from=backend-builder /app/bulk-file-loader .

RUN mkdir -p /app/data && chown appuser:appgroup /app/data

ENV BULK_LOADER_DATA_DIR=/app/data
ENV BULK_LOADER_PORT=8080

EXPOSE 8080

VOLUME ["/app/data"]

USER appuser

# Set the port with BULK_LOADER_PORT (not --port) so the health check finds it.
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
    CMD wget -qO /dev/null "http://127.0.0.1:${BULK_LOADER_PORT:-8080}/healthz" || exit 1

ENTRYPOINT ["./bulk-file-loader", "serve"]
