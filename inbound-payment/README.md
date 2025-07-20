# Go Web Server

A simple HTTP web server built with Go's standard library.

## Features

- RESTful API endpoints
- Health check endpoint
- JSON responses
- Error handling
- Configurable port via environment variable
- Request timeouts and proper HTTP headers

## Endpoints

- `GET /` - Welcome message with API information
- `GET /health` - Health check endpoint
- `GET /api/v1` - API v1 endpoint

## Running the Server

### Local Development

```bash
# Run with default port (8080)
go run main.go

# Run with custom port
PORT=3000 go run main.go
```

### Building

```bash
# Build the binary
go build -o server main.go

# Run the binary
./server
```

### Using Docker

```bash
# Build Docker image
docker build -t go-server .

# Run container
docker run -p 8080:8080 go-server

# Run with custom port
docker run -p 3000:3000 -e PORT=3000 go-server
```

## Environment Variables

- `PORT` - Server port (default: 8080)

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

### API Endpoint
```bash
curl http://localhost:8080/api/v1
```

Response:
```json
{
  "message": "API v1 endpoint",
  "method": "GET",
  "path": "/api/v1",
  "time": "2025-07-20T10:30:00Z"
}
```
