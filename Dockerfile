# Build frontend
FROM node:20-alpine AS frontend-builder

WORKDIR /app/web/ui
COPY web/ui/package*.json ./
RUN npm ci
COPY web/ui/ ./
RUN npm run build

# Build backend
FROM golang:1.25-alpine AS backend-builder

RUN apk add --no-cache git gcc musl-dev

WORKDIR /app

# Install oapi-codegen
RUN go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.1

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Generate API code
RUN oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml

# Copy built frontend
COPY --from=frontend-builder /app/web/ui/dist ./web/ui/dist

# Build
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o bulk-file-loader .

# Runtime image
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=backend-builder /app/bulk-file-loader .

# Create data directory
RUN mkdir -p /app/data

ENV BULK_LOADER_DATA_DIR=/app/data
ENV BULK_LOADER_PORT=8080

EXPOSE 8080

VOLUME ["/app/data"]

ENTRYPOINT ["./bulk-file-loader"]
