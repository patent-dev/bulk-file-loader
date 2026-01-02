.PHONY: generate build test run dev clean

# Generate API code from OpenAPI spec
generate:
	@echo "Generating Go server code..."
	oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml
	@echo "Done."

# Build the application
build: generate
	@echo "Building..."
	cd web/ui && npm ci && npm run build
	go build -o bulk-file-loader .
	@echo "Done."

# Run tests
test:
	go test -v ./...

# Run the application
run:
	go run .

# Start development environment
dev:
	docker compose -f docker-compose.dev.yml up --build

# Clean build artifacts
clean:
	rm -rf bulk-file-loader tmp/ data/*.db web/ui/dist/ web/ui/node_modules/

# Install development tools
tools:
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
	go install github.com/air-verse/air@latest
