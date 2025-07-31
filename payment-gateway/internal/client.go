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
	"sync"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
)

const (
	// healthCheckInterval defines how often health checks are performed.
	healthCheckInterval = 5
	// histogramPrecision defines the precision level for histogram measurements.
	histogramPrecision = 3
)

// ErrPaymentProcessorStatus indicates the payment processor returned an error status.
var ErrPaymentProcessorStatus = errors.New("payment processor returned error status")

// PaymentClient defines the interface for payment processing clients that handle
// payment requests, health monitoring, and graceful shutdown.
type PaymentClient interface {
	ProcessPayment(request *PaymentRequest) error
	GetStatus() PaymentClientState
	Shutdown()
}

// ServicesAvailabilityWireResponse represents the response structure from
// health check endpoints indicating service availability and response times.
type ServicesAvailabilityWireResponse struct {
	Failing         bool `json:"failing"`
	MinResponseTime int  `json:"minResponseTime"`
}

type paymentClient struct {
	httpClient   *http.Client
	state        PaymentClientState
	shutdown     chan struct{}
	serverLogger *slog.Logger

	mu             sync.RWMutex
	url            string
	healthCheckURL string
}

// PaymentClientState holds the current state of a payment client including
// availability status and performance metrics.
type PaymentClientState struct {
	histogram *hdrhistogram.Histogram
	available bool
}

// NewPaymentClient creates a new payment client with health monitoring capabilities.
// It returns a PaymentClient interface for dependency injection and testability.
//
//nolint:ireturn // Interface return is intentional for polymorphism
func NewPaymentClient(httpClient *http.Client, url, healthCheckURL string, logger *slog.Logger) PaymentClient {
	client := &paymentClient{
		httpClient: httpClient,
		state: PaymentClientState{
			available: true,
			histogram: hdrhistogram.New(1, time.Minute.Microseconds(), histogramPrecision),
		},
		shutdown:       make(chan struct{}),
		url:            url,
		healthCheckURL: healthCheckURL,
		serverLogger:   logger,
	}

	go client.monitorClient()

	return client
}

// ProcessPayment sends a payment request to the payment processor.
func (c *paymentClient) ProcessPayment(request *PaymentRequest) error {
	ctx := context.Background()
	since := time.Now()

	var err error

	defer func() {
		timeTaken := time.Since(since)
		c.update(err == nil, timeTaken)
	}()

	request.RequestedAt = time.Now()

	jsonData, err := json.Marshal(request)
	if err != nil {
		c.serverLogger.ErrorContext(ctx, "failed to marshal request: "+err.Error())

		return fmt.Errorf("failed to marshal payment request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.serverLogger.ErrorContext(ctx, "failed to send payment request: "+err.Error())

		return fmt.Errorf("failed to send payment request: %w", err)
	}

	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			c.serverLogger.ErrorContext(ctx, "failed to close response body: "+closeErr.Error())
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %d", ErrPaymentProcessorStatus, resp.StatusCode)
	}

	return nil
}

// GetStatus returns the current state of the payment client.
func (c *paymentClient) GetStatus() PaymentClientState {
	c.mu.RLock()
	localCopy := c.state
	c.mu.RUnlock()

	return localCopy
}

// Shutdown gracefully stops the payment client monitoring.
func (c *paymentClient) Shutdown() {
	close(c.shutdown)
}

func (c *paymentClient) monitorClient() {
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

func (c *paymentClient) update(available bool, timeTaken time.Duration) {
	c.mu.Lock()

	c.state.available = available
	if timeTaken > 0 {
		err := c.state.histogram.RecordValue(timeTaken.Microseconds())
		if err != nil {
			// Log error but don't fail the update operation
			ctx := context.Background()
			c.serverLogger.ErrorContext(ctx, "failed to record histogram value: "+err.Error())
		}
	}

	c.mu.Unlock()
}

func (c *paymentClient) performHealthCheck() {
	ctx := context.Background()
	c.serverLogger.DebugContext(ctx, "health checking endpoint: "+c.healthCheckURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.healthCheckURL, nil)
	if err != nil {
		c.serverLogger.ErrorContext(ctx, "failed to create health check request: "+err.Error())
		c.update(false, -1)

		return
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.serverLogger.ErrorContext(ctx, "endpoint "+c.healthCheckURL+" unavailable")
		c.update(false, -1)

		return
	}

	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			c.serverLogger.ErrorContext(ctx, "failed to close response body: "+closeErr.Error())
		}
	}()

	if resp.StatusCode != http.StatusOK {
		c.serverLogger.ErrorContext(ctx, "endpoint "+c.healthCheckURL+" unavailable")
		c.update(false, -1)

		return
	}

	var healthResp ServicesAvailabilityWireResponse

	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	if err != nil {
		c.update(false, -1)

		return
	}

	available := !healthResp.Failing

	var duration time.Duration
	if healthResp.MinResponseTime > 0 {
		duration = time.Duration(healthResp.MinResponseTime) * time.Millisecond
	}

	c.serverLogger.DebugContext(ctx, "health-check completed",
		slog.String("endpoint", c.healthCheckURL),
		slog.Bool("available", available),
		slog.Int("response_time_ms", healthResp.MinResponseTime))

	c.update(available, duration)
}
