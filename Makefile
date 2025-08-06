.PHONY: build run run-dev test test-local clean docker-build docker-run up down dc-build post-payment get-summary test-endpoints

# Variables
BINARY_NAME=server
DOCKER_IMAGE=go-server
PORT=8080
PAYMENT_GATEWAY_DIR=payment-gateway

# Payment processor configuration
PRIMARY_PAYMENT_PROCESSOR_URL=http://payment-processor.example.com/process
PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL=http://payment-processor.example.com/health_check
FALLBACK_PAYMENT_PROCESSOR_URL=http://payment-processor-fallback.example.com/process
FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL=http://payment-processor-fallback.example.com/health_check

# Build the application
build:
	@echo "Building $(BINARY_NAME)..."
	@cd $(PAYMENT_GATEWAY_DIR) && go build -o $(BINARY_NAME) .

# Run the application
run:
	@echo "Running server on port $(PORT)..."
	@cd $(PAYMENT_GATEWAY_DIR) && PORT=$(PORT) \
	 PRIMARY_PAYMENT_PROCESSOR_URL=$(PRIMARY_PAYMENT_PROCESSOR_URL) \
	 PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL=$(PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL) \
	 FALLBACK_PAYMENT_PROCESSOR_URL=$(FALLBACK_PAYMENT_PROCESSOR_URL) \
	 FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL=$(FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL) \
	 go run main.go

# Run with development settings
run-dev:
	@echo "Running server in development mode on port $(PORT)..."
	@cd $(PAYMENT_GATEWAY_DIR) && PORT=$(PORT) \
	 PRIMARY_PAYMENT_PROCESSOR_URL=http://localhost:9001/payments \
	 PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL=http://localhost:9001/payments/service-health \
	 FALLBACK_PAYMENT_PROCESSOR_URL=http://localhost:9002/payments \
	 FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL=http://localhost:9002/payments/service-health \
	 go run main.go

# Run tests (when tests are added)
test:
	@echo "Running tests..."
	@cd $(PAYMENT_GATEWAY_DIR) && go test -v ./...

# Test local deployment with curl
post-payment:
	@echo "Testing payment endpoint via Envoy load balancer..."
	@echo "Making POST request to http://localhost:9999/payments"
	@curl -X POST http://localhost:9999/payments \
		-H "Content-Type: application/json" \
		-d '{"correlationId": "$(shell uuidgen)", "amount": 100.50}' \
		-w "\nHTTP Status: %{http_code}\nResponse Time: %{time_total}s\n" \
		-s || echo "Error: Make sure the services are running with 'make up'"

# Test payments summary endpoint
get-summary:
	@echo "Testing payments summary endpoint via Envoy load balancer..."
	@echo "Making GET request to http://localhost:9999/payments-summary"
	@curl -X GET http://localhost:9999/payments-summary \
		-H "Accept: application/json" \
		-w "\nHTTP Status: %{http_code}\nResponse Time: %{time_total}s\n" \
		-s || echo "Error: Make sure the services are running with 'make up'"

# Test both endpoints in sequence
test-endpoints: post-payment get-summary
	@echo "Testing complete! Both endpoints tested."

# Build Docker image
docker-build:
	@echo "Building Docker image $(DOCKER_IMAGE)..."
	@cd $(PAYMENT_GATEWAY_DIR) && docker build -t $(DOCKER_IMAGE) .

# Run Docker container
docker-run: docker-build
	@echo "Running Docker container on port $(PORT)..."
	@docker run --rm -p $(PORT):8080 \
	 -e PRIMARY_PAYMENT_PROCESSOR_URL=$(PRIMARY_PAYMENT_PROCESSOR_URL) \
	 -e PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL=$(PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL) \
	 -e FALLBACK_PAYMENT_PROCESSOR_URL=$(FALLBACK_PAYMENT_PROCESSOR_URL) \
	 -e FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL=$(FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL) \
	 $(DOCKER_IMAGE)

dc-build:
	@echo "building all services with Docker Compose..."
	@docker-compose build

up: dc-build
	@echo "Starting all services with Docker Compose..."
	@docker-compose up -d

down:
	@echo "Stopping all services..."
	@docker-compose down

# Show help
help:
	@echo "Available commands:"
	@echo "  build        - Build the application"
	@echo "  run          - Run the application with production settings"
	@echo "  run-dev      - Run the application with development settings"
	@echo "  test         - Run unit tests"
	@echo "  post-payment - Test payment endpoint via Envoy load balancer (requires services running)"
	@echo "  get-summary  - Test payments summary endpoint via Envoy load balancer (requires services running)"
	@echo "  test-endpoints - Test both payment and summary endpoints (requires services running)"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-run   - Build and run Docker container"
	@echo "  dc-build    - build all services with Docker Compose"
	@echo "  up    - Start all services with Docker Compose"
	@echo "  down  - Stop all services"
	@echo "  help         - Show this help"
	@echo ""
	@echo "Environment Variables:"
	@echo "  PORT                                    - Server port (default: $(PORT))"
	@echo "  PRIMARY_PAYMENT_PROCESSOR_URL           - Primary payment processor URL"
	@echo "  PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL    - Primary payment processor health check URL"
	@echo "  FALLBACK_PAYMENT_PROCESSOR_URL          - Fallback payment processor URL"
	@echo "  FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL   - Fallback payment processor health check URL"
	@echo ""
	@echo "Quick Start:"
	@echo "  make up           # Start all services"
	@echo "  make post-payment # Test the payment endpoint"
	@echo "  make get-summary  # Test the payments summary endpoint"
	@echo "  make test-endpoints # Test both endpoints"
	@echo "  make down         # Stop services"
