# Build frontend
FROM node:20-alpine AS frontend-builder

WORKDIR /app/web/ui
COPY web/ui/package*.json ./
RUN npm ci
COPY web/ui/ ./
RUN npm run build

# Build backend
FROM golang:1.26-alpine AS backend-builder

RUN apk add --no-cache git

WORKDIR /app

# Install oapi-codegen
RUN go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.6.0

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
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

COPY --from=backend-builder /app/bulk-file-loader .

RUN mkdir -p /app/data && chown appuser:appgroup /app/data

ENV BULK_LOADER_DATA_DIR=/app/data
ENV BULK_LOADER_PORT=8080

EXPOSE 8080

VOLUME ["/app/data"]

USER appuser

ENTRYPOINT ["./bulk-file-loader", "serve"]
