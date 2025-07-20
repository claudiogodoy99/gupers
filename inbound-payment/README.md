# Inbound Payment Service

A robust payment processing service built with Go, featuring dual payment processor support with automatic failover, real-time latency monitoring, and health checking.

## Features

- **Dual Payment Processor Support**: Primary and fallback payment processors with automatic failover
- **Real-time Latency Monitoring**: HDR histogram-based latency tracking with P95 metrics
- **Health Monitoring**: Periodic health checks every 5 seconds for both processors
- **Circuit Breaker Pattern**: Automatic availability tracking and state management
- **Structured Logging**: Comprehensive logging with Zap logger and correlation IDs
- **Graceful Shutdown**: Proper cleanup of background monitoring goroutines
- **Docker Support**: Containerized deployment with environment variable configuration

## Architecture

The service implements a resilient payment processing architecture:

1. **PaymentHandler**: Main business logic that orchestrates payment processing
2. **PaymentClient**: HTTP client wrapper with monitoring and health checking
3. **Background Monitoring**: Continuous health checks and latency tracking
4. **Automatic Failover**: Switches to fallback processor when primary is unavailable

## Endpoints

- `GET /health` - Service health check endpoint
- `POST /payments` - Process payment requests

## Payment Request Format

```json
{
  "amount": 100.50,
  "correlation_id": "unique-correlation-id"
}
```

## Running the Service

### Using Make (Recommended)

```bash
# Run with production settings
make run

# Run with development settings (localhost payment processors)
make run-dev

# Build the application
make build

# Run tests
make test

# View all available commands
make help
```

### Local Development

```bash
# Run with default settings
go run main.go

# Run with custom configuration
PORT=3000 \
PRIMARY_PAYMENT_PROCESSOR_URL=http://localhost:9001/process \
PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL=http://localhost:9001/health_check \
FALLBACK_PAYMENT_PROCESSOR_URL=http://localhost:9002/process \
FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL=http://localhost:9002/health_check \
go run main.go
```

### Building

```bash
# Build the binary
make build
# or
go build -o server .

# Run the binary
./server
```

### Using Docker

```bash
# Build and run with Docker
make docker-run

# Or manually:
docker build -t inbound-payment .
docker run -p 8080:8080 \
  -e PRIMARY_PAYMENT_PROCESSOR_URL=http://payment-processor:8080/process \
  -e PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL=http://payment-processor:8080/health_check \
  -e FALLBACK_PAYMENT_PROCESSOR_URL=http://fallback-processor:8080/process \
  -e FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL=http://fallback-processor:8080/health_check \
  inbound-payment
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Server port | `8080` |
| `PRIMARY_PAYMENT_PROCESSOR_URL` | Primary payment processor endpoint | `http://payment-processor.example.com/process` |
| `PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL` | Primary payment processor health check | `http://payment-processor.example.com/health_check` |
| `FALLBACK_PAYMENT_PROCESSOR_URL` | Fallback payment processor endpoint | `http://payment-processor-fallback.example.com/process` |
| `FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL` | Fallback payment processor health check | `http://payment-processor-fallback.example.com/health_check` |
| `GIN_MODE` | Gin framework mode (`debug` or `release`) | `release` |

## API Examples

### Health Check

```bash
curl http://localhost:8080/health
```

Response:

```json
{
  "status": "ok",
  "timestamp": "2025-07-20T10:30:00Z",
  "message": "Go web server is running"
}
```

### Process Payment

```bash
curl -X POST http://localhost:8080/payments \
  -H "Content-Type: application/json" \
  -d '{
    "amount": 100.50,
    "correlation_id": "payment-123-456"
  }'
```

Success Response:

```json
{
  "status": "processed"
}
```

Error Response:

```json
{
  "error": "Failed to process payment"
}
```

## Monitoring and Observability

The service provides comprehensive monitoring through structured logging:

- **Request Tracing**: Each payment request includes a correlation ID for tracking
- **Latency Metrics**: Real-time P95 latency calculation using HDR histograms
- **Health Status**: Continuous monitoring of payment processor availability
- **Error Tracking**: Detailed error logging with context and correlation IDs

### Log Examples

```json
{
  "level": "info",
  "ts": "2025-07-20T10:30:00.123Z",
  "msg": "Payment processed successfully",
  "correlation_id": "payment-123-456",
  "latency_ms": 45
}
```

## Development

### Prerequisites

- Go 1.21 or later
- Docker (optional)
- Make (optional, but recommended)

### Dependencies

Key dependencies include:

- **Gin**: HTTP web framework
- **Zap**: Structured logging
- **HDR Histogram**: Latency distribution tracking

Install dependencies:

```bash
make deps
# or
go mod download && go mod tidy
```

### Code Quality

```bash
# Format code
make fmt

# Run linter
make lint

# Run tests
make test
```

## Deployment

### Docker Compose Example

```yaml
version: '3.8'
services:
  inbound-payment:
    build: .
    ports:
      - "8080:8080"
    environment:
      - PRIMARY_PAYMENT_PROCESSOR_URL=http://payment-processor:8080/process
      - PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL=http://payment-processor:8080/health_check
      - FALLBACK_PAYMENT_PROCESSOR_URL=http://fallback-processor:8080/process
      - FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL=http://fallback-processor:8080/health_check
    depends_on:
      - payment-processor
      - fallback-processor
```

## Architecture Decision Records

### Payment Processing Flow

1. **Primary Processor Check**: Service checks if primary processor is available
2. **Primary Attempt**: If available, attempts payment processing
3. **Fallback Logic**: On primary failure or unavailability, switches to fallback
4. **Error Handling**: Returns appropriate errors if both processors fail
5. **Metrics Recording**: Records latency and updates availability status

### Health Monitoring

- Health checks run every 5 seconds in background goroutines
- Availability status is updated based on HTTP response codes
- Latency measurements are continuously recorded using HDR histograms
- P95 latency is calculated in real-time for performance monitoring
