# Gupers - Rinha de Backend 2025

[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://golang.org/)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED?style=flat&logo=docker)](https://docker.com/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

## 🏁 Rinha de Backend 2025 Challenge

This repository contains my submission for the **Rinha de Backend 2025** challenge - a high-performance payment intermediation service built with Go. The challenge requires building a backend that intermediates payment requests to payment processing services while handling instabilities, failures, and optimizing for the lowest processing fees.

## 🎯 Challenge Overview

The service acts as an intermediary between clients and payment processors, featuring:

- **Primary Payment Processor**: Lower fees but may become unstable
- **Fallback Payment Processor**: Higher fees but provides backup when primary fails
- **Smart Routing**: Automatically routes to the best available processor
- **Performance Optimization**: Aims for sub-11ms p99 response times for bonus scoring
- **Consistency Auditing**: Maintains transaction consistency for regulatory compliance

## 🏆 Scoring Criteria

- **Primary**: Profit maximization through lowest fees
- **Penalty**: 35% fine for transaction inconsistencies
- **Bonus**: 2% bonus per millisecond under 11ms p99 response time
- **Formula**: `(11 - p99) * 0.02` for performance bonus

## 🏗️ Architecture

```
[Client] → [Load Balancer] → [Backend Instances] → [Payment Processors]
                ↓                    ↓                      ↓
            [nginx]              [Go Service]        [Primary/Fallback]
```

### Key Components

1. **Payment Handler**: Orchestrates payment processing logic
2. **Payment Client**: HTTP client with monitoring and health checking
3. **Circuit Breaker**: Automatic failover between processors
4. **Health Monitor**: Continuous availability tracking (every 5 seconds)
5. **Metrics Collection**: Real-time latency distribution with HDR histograms

## 🚀 Quick Start

### Prerequisites

- Docker & Docker Compose
- Go 1.21+ (for local development)
- Make (recommended)

### Running the Complete Stack

1. **Start Payment Processors**:
```bash
# Clone payment processors
git clone https://github.com/rinha-de-backend/payment-processor-2025
cd payment-processor-2025
docker-compose up -d
```

2. **Start the Backend**:
```bash
# In this repository
docker-compose up -d
```

3. **Verify Services**:
```bash
# Check backend health
curl http://localhost:9999/health

# Check payment processors
curl http://localhost:8001/payments/service-health
curl http://localhost:8002/payments/service-health
```

## 📊 API Endpoints

### Backend Endpoints (Port 9999)

#### Process Payment
```http
POST /payments
Content-Type: application/json

{
    "correlationId": "4a7901b8-7d26-4d9d-aa19-4dc1c7cf60b3",
    "amount": 19.90
}
```

#### Payments Summary
```http
GET /payments-summary?from=2020-07-10T12:34:56.000Z&to=2020-07-10T12:35:56.000Z
```

Response:
```json
{
    "default": {
        "totalRequests": 43236,
        "totalAmount": 415542345.98
    },
    "fallback": {
        "totalRequests": 423545,
        "totalAmount": 329347.34
    }
}
```

## 🛠️ Technology Stack

- **Language**: Go 1.21+
- **Framework**: Gin (HTTP router)
- **Logging**: Zap (structured logging)
- **Metrics**: HDR Histogram (latency distribution)
- **Containerization**: Docker & Docker Compose
- **Load Balancer**: Nginx
- **Architecture**: Microservices with circuit breaker pattern

## 🔧 Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Server port | `8080` |
| `PRIMARY_PAYMENT_PROCESSOR_URL` | Primary processor endpoint | `http://payment-processor-default:8080/payments` |
| `FALLBACK_PAYMENT_PROCESSOR_URL` | Fallback processor endpoint | `http://payment-processor-fallback:8080/payments` |
| `PRIMARY_HEALTH_URL` | Primary health check endpoint | `http://payment-processor-default:8080/payments/service-health` |
| `FALLBACK_HEALTH_URL` | Fallback health check endpoint | `http://payment-processor-fallback:8080/payments/service-health` |

### Resource Limits

As per challenge requirements:

- **Total CPU**: 1.5 cores
- **Total Memory**: 350MB
- **Network**: Bridge mode only
- **Port**: 9999 (exposed)

## 📈 Performance Features

### Smart Payment Routing

1. Check primary processor availability via health endpoint
2. Route to primary if available (lower fees)
3. Automatic fallback on primary failure/unavailability
4. Real-time latency tracking and P95 calculation

### Health Monitoring

- Background health checks every 5 seconds
- Respects rate limits (1 call per 5 seconds per processor)
- Circuit breaker pattern implementation
- Automatic processor state management

### Observability

- Structured logging with correlation IDs
- Real-time latency distribution (HDR histograms)
- Performance metrics (P95, P99)
- Request tracing and error tracking

## 🧪 Local Development

### Development Setup

```bash
# Install dependencies
make deps

# Run in development mode
make run-dev

# Run tests
make test

# Code quality
make fmt && make lint
```

### Testing with Mock Processors

```bash
# Start local mock servers on ports 9001, 9002
make run-dev
```

## 🐳 Docker Deployment

### Production Deployment

```bash
# Build and run
docker-compose up -d

# Scale backend instances
docker-compose up -d --scale backend=2
```

### Docker Compose Structure

```yaml
services:
  nginx:          # Load balancer (Port 9999)
  inbound-payment:        # Go application instances
```

## 📋 Submission Checklist

- [x] Docker Compose with resource limits
- [x] Two backend instances with load balancer
- [x] Port 9999 exposure
- [x] Payment processing endpoint
- [x] Payments summary endpoint
- [x] Circuit breaker with fallback logic
- [x] Health check integration
- [x] Performance optimization (sub-11ms target)
- [x] Transaction consistency
- [x] Structured logging and monitoring

## 🎮 Challenge Strategy

### Fee Optimization

- Prioritize primary processor (lower fees)
- Quick failover to minimize high-fee transactions
- Health check optimization within rate limits

### Performance Optimization

- Target p99 < 11ms for maximum bonus
- Efficient HTTP client configuration
- Concurrent request handling
- Minimal allocation patterns

### Reliability

- Circuit breaker implementation
- Graceful degradation
- Transaction consistency maintenance
- Comprehensive error handling

## 📚 Project Structure

```
├── inbound-payment/          # Main service
│   ├── main.go              # Application entry point
│   ├── pkg/                 # Core packages
│   │   ├── client.go        # Payment client with monitoring
│   │   └── payment.go       # Payment processing logic
│   ├── docker-compose.yml   # Service orchestration
│   └── Makefile            # Build and run commands
├── nginx/                   # Load balancer configuration
├── scripts/                 # Deployment and test scripts
└── docs/                   # Additional documentation
```

## 🤝 Contributing

This is a competition submission, but feedback and suggestions are welcome!

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Submit a pull request

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🔗 Links

- [Rinha de Backend 2025](https://github.com/rinha-de-backend/rinha-de-backend-2025)
- [Payment Processor](https://github.com/rinha-de-backend/payment-processor-2025)
- [Challenge Specifications](https://github.com/rinha-de-backend/rinha-de-backend-2025/blob/main/README.md)

---

**Built with ❤️ for Rinha de Backend 2025**
