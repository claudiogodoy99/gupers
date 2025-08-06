// Package internal provides payment client functionality for the payment gateway service.
package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
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
	httpClient   *http.Client
	count        atomic.Int64
	health       atomic.Bool
	shutdown     chan struct{}
	serverLogger *slog.Logger

	url            string
	healthCheckURL string
}

// NewPaymentClient creates a new PaymentClient instance for interacting with the payment gateway.
func NewPaymentClient(httpClient *http.Client, url, healthCheckURL string, logger *slog.Logger) *PaymentClient {
	client := &PaymentClient{
		httpClient: httpClient,

		shutdown:       make(chan struct{}),
		url:            url,
		healthCheckURL: healthCheckURL,
		serverLogger:   logger,
	}

	go client.monitorClient()

	return client
}

// Shutdown gracefully stops the payment client monitoring.
func (c *PaymentClient) Shutdown() {
	close(c.shutdown)
}

func (c *PaymentClient) processPayment(request *PaymentRequest) error {
	c.count.Add(1)

	ctx := context.Background()

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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.serverLogger.ErrorContext(ctx, fmt.Sprintf("failed to send payment request: %v", err))

		return fmt.Errorf("failed to send payment request: %w", err)
	}

	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			c.serverLogger.ErrorContext(ctx, fmt.Sprintf("failed to close response body: %v", closeErr))
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %d", ErrPaymentProcessorStatus, resp.StatusCode)
	}

	return nil
}

func (c *PaymentClient) monitorClient() {
	ticker := time.NewTicker(healthCheckInterval * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.performHealthCheck()
		case <-c.shutdown:
			return
		}
	}
}

func (c *PaymentClient) update(available bool) {
	c.health.Store(available)
}

func (c *PaymentClient) performHealthCheck() {
	ctx := context.Background()
	c.serverLogger.DebugContext(ctx, "health checking endpoint: "+c.healthCheckURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.healthCheckURL, nil)
	if err != nil {
		c.serverLogger.ErrorContext(ctx, fmt.Sprintf("failed to create health check request: %v", err))
		c.update(false)

		return
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.serverLogger.ErrorContext(ctx, "endpoint "+c.healthCheckURL+" unavailable")
		c.update(false)

		return
	}

	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			c.serverLogger.ErrorContext(ctx, fmt.Sprintf("failed to close response body: %v", closeErr))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		c.serverLogger.ErrorContext(ctx, "endpoint "+c.healthCheckURL+" unavailable")

		return
	}

	var healthResp ServicesAvailabilityWireResponse

	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	if err != nil {
		return
	}

	available := !healthResp.Failing

	c.serverLogger.DebugContext(ctx, "health-check completed",
		slog.String("endpoint", c.healthCheckURL),
		slog.Bool("available", available),
		slog.Int("response_time_ms", healthResp.MinResponseTime))

	c.update(available)
}
