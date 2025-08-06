package internal

import (
	"context"
	"log/slog"
	"time"
)

const (
	tickerInterval = 100
)

// PaymentHandler manages payment processing with primary and fallback clients
// using a fan-in/fan-out pattern for latency.
type PaymentHandler struct {
	logger             *slog.Logger
	pendingPaymentChan chan *PaymentRequest
	router             Router
	shutdownChan       chan struct{}
}

// NewPaymentHandler creates a new payment handler with the specified clients and worker configuration.
// It starts the specified number of workers to process payments asynchronously.
func NewPaymentHandler(
	router *Router,
	numWorkers int,
	pendingPaymentChan chan *PaymentRequest,
	logger *slog.Logger,
) *PaymentHandler {
	payment := &PaymentHandler{
		router:             *router,
		logger:             logger,
		pendingPaymentChan: pendingPaymentChan,
		shutdownChan:       make(chan struct{}),
	}

	for range numWorkers {
		go payment.processPaymentAsync()
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

	ticker := time.Tick(tickerInterval * time.Millisecond)

	for {
		select {
		case p.pendingPaymentChan <- request:
			return nil
		case <-ticker:
			p.logger.WarnContext(ctx, "taking too long to add request on channel",
				slog.Int("channel_len", len(p.pendingPaymentChan)))
		case <-p.shutdownChan:
			return nil
		}
	}
}

// Shutdown gracefully shuts down the PaymentHandler and its associated router.
func (p *PaymentHandler) Shutdown() {
	p.router.Shutdown()

	close(p.shutdownChan)
}

func (p *PaymentHandler) processPaymentAsync() {
	ctx := context.Background()

	for {
		select {
		case request := <-p.pendingPaymentChan:
			client := p.router.route()

			err := client.processPayment(request)
			if err != nil {
				p.logger.InfoContext(ctx, "error on pending request, trying to put on channel again")

				go func() {
					_ = p.ProcessPayment(ctx, request)
				}()
			}
		case <-p.shutdownChan:
			return
		}
	}
}
