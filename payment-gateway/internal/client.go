// Package internal provides the implementation for the payment gateway client and related utilities.
package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
)

const (
	// healthCheckInterval defines how often health checks are performed.
	healthCheckInterval = 5
)

// ErrPaymentProcessorStatus indicates the payment processor returned an error status.
var ErrPaymentProcessorStatus = errors.New("payment processor returned error status")

// ServicesAvailabilityWireResponse represents the response structure from
// health check endpoints indicating service availability and response times.
type ServicesAvailabilityWireResponse struct {
	Failing         bool `json:"failing"`
	MinResponseTime int  `json:"minResponseTime"`
}

// PaymentClient is a client for the payment gateway.
type PaymentClient struct {
	httpClient            *fasthttp.Client
	count                 atomic.Int64
	health                atomic.Bool
	shutdown              chan struct{}
	serverLogger          *slog.Logger
	headerContentTypeJSON []byte
	url                   string
	healthCheckURL        string
}

// NewPaymentClient creates a new PaymentClient instance for interacting with the payment gateway.
func NewPaymentClient(ctx context.Context,
	httpClient *fasthttp.Client, url, healthCheckURL string,
	logger *slog.Logger,
) *PaymentClient {
	client := &PaymentClient{
		httpClient:            httpClient,
		shutdown:              make(chan struct{}),
		url:                   url,
		healthCheckURL:        healthCheckURL,
		headerContentTypeJSON: []byte("application/json"),
		serverLogger:          logger,
	}

	go client.monitorClient(ctx)

	return client
}

// Shutdown gracefully stops the payment client monitoring.
func (c *PaymentClient) Shutdown() {
	close(c.shutdown)
}

func (c *PaymentClient) processPayment(ctx context.Context, request *PaymentRequest) error {
	c.count.Add(1)

	var err error

	defer func() {
		c.update(err == nil)
	}()

	request.RequestedAt = time.Now()

	jsonData, err := json.Marshal(request)
	if err != nil {
		c.serverLogger.ErrorContext(ctx, fmt.Sprintf("failed to marshal request: %v", err))

		return fmt.Errorf("failed to marshal payment request: %w", err)
	}

	req := fasthttp.AcquireRequest()
	req.SetRequestURI(c.url)
	req.Header.SetMethod(fasthttp.MethodPost)
	req.Header.SetContentTypeBytes(c.headerContentTypeJSON)
	req.SetBodyRaw(jsonData)

	resp := fasthttp.AcquireResponse()
	err = c.httpClient.Do(req, resp)

	fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	if err != nil {
		c.serverLogger.ErrorContext(ctx, fmt.Sprintf("failed to send payment request: %v", err))

		return ErrPaymentProcessorStatus
	}

	stat := resp.StatusCode()

	if stat < 200 || stat >= 300 {
		return ErrPaymentProcessorStatus
	}

	return nil
}

func (c *PaymentClient) monitorClient(ctx context.Context) {
	ticker := time.NewTicker(healthCheckInterval * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.performHealthCheck(ctx)
		case <-c.shutdown:
			return
		case <-ctx.Done():
			close(c.shutdown)

			return
		}
	}
}

func (c *PaymentClient) update(available bool) {
	c.health.Store(available)
}

func (c *PaymentClient) performHealthCheck(ctx context.Context) {
	c.serverLogger.DebugContext(ctx, "health checking endpoint: "+c.healthCheckURL)

	req := fasthttp.AcquireRequest()
	req.SetRequestURI(c.healthCheckURL)
	req.Header.SetMethod(fasthttp.MethodGet)

	resp := fasthttp.AcquireResponse()
	err := c.httpClient.Do(req, resp)

	fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	if err != nil {
		c.update(false)

		return
	}

	statusCode := resp.StatusCode()

	if statusCode != http.StatusOK {
		c.update(false)

		return
	}

	respBody := resp.Body()

	healthResp := &ServicesAvailabilityWireResponse{
		Failing: false,
	}

	err = json.Unmarshal(respBody, healthResp)
	if err != nil {
		c.update(false)

		return
	}

	available := !healthResp.Failing

	c.serverLogger.DebugContext(ctx, "health-check completed",
		slog.String("endpoint", c.healthCheckURL),
		slog.Bool("available", available))

	c.update(available)
}
