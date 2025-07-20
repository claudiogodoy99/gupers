.PHONY: build run run-dev test clean docker-build docker-run fmt lint deps help

# Variables
BINARY_NAME=server
DOCKER_IMAGE=go-server
PORT=8080

# Payment processor configuration
PRIMARY_PAYMENT_PROCESSOR_URL=http://payment-processor.example.com/process
PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL=http://payment-processor.example.com/health_check
FALLBACK_PAYMENT_PROCESSOR_URL=http://payment-processor-fallback.example.com/process
FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL=http://payment-processor-fallback.example.com/health_check

# Build the application
build:
	@echo "Building $(BINARY_NAME)..."
	@go build -o $(BINARY_NAME) .

# Run the application
run:
	@echo "Running server on port $(PORT)..."
	@PORT=$(PORT) \
	 PRIMARY_PAYMENT_PROCESSOR_URL=$(PRIMARY_PAYMENT_PROCESSOR_URL) \
	 PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL=$(PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL) \
	 FALLBACK_PAYMENT_PROCESSOR_URL=$(FALLBACK_PAYMENT_PROCESSOR_URL) \
	 FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL=$(FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL) \
	 go run main.go

# Run with development settings
run-dev:
	@echo "Running server in development mode on port $(PORT)..."
	@PORT=$(PORT) \
	 GIN_MODE=debug \
	 PRIMARY_PAYMENT_PROCESSOR_URL=http://localhost:9001/process \
	 PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL=http://localhost:9001/health_check \
	 FALLBACK_PAYMENT_PROCESSOR_URL=http://localhost:9002/process \
	 FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL=http://localhost:9002/health_check \
	 go run main.go

# Run tests (when tests are added)
test:
	@echo "Running tests..."
	@go test -v ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -f $(BINARY_NAME)
	@go clean

# Build Docker image
docker-build:
	@echo "Building Docker image $(DOCKER_IMAGE)..."
	@docker build -t $(DOCKER_IMAGE) .

# Run Docker container
docker-run: docker-build
	@echo "Running Docker container on port $(PORT)..."
	@docker run --rm -p $(PORT):8080 \
	 -e PRIMARY_PAYMENT_PROCESSOR_URL=$(PRIMARY_PAYMENT_PROCESSOR_URL) \
	 -e PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL=$(PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL) \
	 -e FALLBACK_PAYMENT_PROCESSOR_URL=$(FALLBACK_PAYMENT_PROCESSOR_URL) \
	 -e FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL=$(FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL) \
	 $(DOCKER_IMAGE)

# Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...

# Lint code (requires golangci-lint)
lint:
	@echo "Linting code..."
	@golangci-lint run

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	@go mod download
	@go mod tidy

# Show help
help:
	@echo "Available commands:"
	@echo "  build        - Build the application"
	@echo "  run          - Run the application with production settings"
	@echo "  run-dev      - Run the application with development settings"
	@echo "  test         - Run tests"
	@echo "  clean        - Clean build artifacts"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-run   - Build and run Docker container"
	@echo "  fmt          - Format code"
	@echo "  lint         - Lint code"
	@echo "  deps         - Download and tidy dependencies"
	@echo "  help         - Show this help"
	@echo ""
	@echo "Environment Variables:"
	@echo "  PORT                                    - Server port (default: $(PORT))"
	@echo "  PRIMARY_PAYMENT_PROCESSOR_URL           - Primary payment processor URL"
	@echo "  PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL    - Primary payment processor health check URL"
	@echo "  FALLBACK_PAYMENT_PROCESSOR_URL          - Fallback payment processor URL"
	@echo "  FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL   - Fallback payment processor health check URL"
	@echo "  GIN_MODE                                - Gin mode (debug/release)"
