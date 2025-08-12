package internal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"
)

const (
	tickerInterval = 100
)

// ErrDBClientUnavailable is returned when a database client hasn't been configured.
var ErrDBClientUnavailable = errors.New("database client not available")

// PaymentHandler manages payment processing with primary and fallback clients
// using a fan-in/fan-out pattern for latency.
type PaymentHandler struct {
	router             *Router
	logger             *slog.Logger
	pendingPaymentChan chan *PaymentRequest
	shutdown           chan struct{}
	dbClient           DBClient
}

// NewPaymentHandler creates a new payment handler with the specified clients and worker configuration.
// It starts the specified number of workers to process payments asynchronously.
func NewPaymentHandler(ctx context.Context,
	router *Router,
	numWorkers int,
	pendingChan chan *PaymentRequest,
	logger *slog.Logger,
	dbClient DBClient,
) *PaymentHandler {
	payment := &PaymentHandler{
		router:             router,
		logger:             logger,
		pendingPaymentChan: pendingChan,
		shutdown:           make(chan struct{}),
		dbClient:           dbClient,
	}

	for range numWorkers {
		go payment.processPaymentWorker(ctx)
	}

	return payment
}

// PaymentRequest represents a payment processing request with amount, correlation ID, and timestamp.
type PaymentRequest struct {
	Amount        float64   `json:"amount"`
	CorrelationID string    `json:"correlationId"`
	RequestedAt   time.Time `json:"requestedAt"`
}

// ProcessPayment adds a payment request to the pending channel for processing.
func (p *PaymentHandler) ProcessPayment(ctx context.Context, request *PaymentRequest) error {
	p.logger.DebugContext(ctx, "received request, adding into channel",
		slog.Int("channel_len", len(p.pendingPaymentChan)))

	ticker := time.NewTicker(tickerInterval * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case p.pendingPaymentChan <- request:
			return nil
		case <-ticker.C:
			p.logger.WarnContext(ctx, "taking too long to add request on channel",
				slog.Int("channel_len", len(p.pendingPaymentChan)))
		case <-p.shutdown:
			return nil
		}
	}
}

// GetPaymentsSummary returns payment summaries for the provided time window.
func (p *PaymentHandler) GetPaymentsSummary(ctx context.Context, from, to time.Time) ([2]Summary, error) {
	if p.dbClient == nil {
		return [2]Summary{}, ErrDBClientUnavailable
	}

	summaries, err := p.dbClient.Read(ctx, from, to)
	if err != nil {
		return [2]Summary{}, fmt.Errorf("read payments summary: %w", err)
	}

	return summaries, nil
}

// Shutdown gracefully shuts down the PaymentHandler and its associated router.
func (p *PaymentHandler) Shutdown() {
	p.router.Shutdown()

	close(p.shutdown)
}

func (p *PaymentHandler) processPaymentWorker(ctx context.Context) {
	for {
		select {
		case request := <-p.pendingPaymentChan:
			routeClientAndSendRequest := func() error {
				client := p.router.route()

				p.logger.InfoContext(ctx, "Processing payment",
					slog.String("correlationId", request.CorrelationID),
					slog.Float64("amount", request.Amount))

				return client.processPayment(ctx, request)
			}
			persistRequestInDatabase := func() error {
				return p.sendToDB(ctx, request)
			}

			// We must retry until the both func's are successfully done
			retryEngine(routeClientAndSendRequest)
			retryEngine(persistRequestInDatabase)

			p.logger.InfoContext(ctx, "Payment processor succeeded",
				slog.String("correlationId", request.CorrelationID))
		case <-p.shutdown:
			return
		}
	}
}

func (p *PaymentHandler) sendToDB(ctx context.Context, request *PaymentRequest) error {
	err := p.dbClient.Write(ctx, *request)
	if err == nil {
		return nil
	}

	p.logger.ErrorContext(ctx, "failed to write payment request to database",
		slog.String("correlationId", request.CorrelationID),
		slog.String("error", err.Error()))

	return fmt.Errorf("write payment request: %w", err)
}

func retryEngine(action func() error) {
	const (
		initialBackoff     = 10 * time.Millisecond
		maxBackoff         = 5 * time.Second
		maxRandomIncrement = 100
	)

	backoff := initialBackoff

	for {
		err := action()
		if err == nil {
			return
		}

		//nolint: gosec
		// Use of weak random number generator.
		// See https://cwe.mitre.org/data/definitions/338.html.
		// The issue is valid when using in security context.
		randomIncrement := time.Duration(rand.IntN(maxRandomIncrement)) * time.Millisecond
		time.Sleep(backoff + randomIncrement)

		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}
