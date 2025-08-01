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
	paymentProcessorClient         *PaymentClient
	paymentProcessorFallbackClient *PaymentClient
	logger                         *slog.Logger
	pendingPaymentChan             chan *PaymentRequest
	shutdown                       chan struct{}
}

// NewPaymentHandler creates a new payment handler with the specified clients and worker configuration.
// It starts the specified number of workers to process payments asynchronously.
func NewPaymentHandler(
	paymentProcessorClient, paymentProcessorFallbackClient *PaymentClient,
	chanLen, numWorkers int,
	logger *slog.Logger,
) *PaymentHandler {
	payment := &PaymentHandler{
		paymentProcessorClient:         paymentProcessorClient,
		paymentProcessorFallbackClient: paymentProcessorFallbackClient,
		logger:                         logger,
		pendingPaymentChan:             make(chan *PaymentRequest, chanLen),
		shutdown:                       make(chan struct{}),
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

// ProcessPayment queues a payment request for asynchronous processing.
// It returns an error if the system is shutting down.
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
		case <-p.shutdown:
			return nil
		}
	}
}

// Shutdown gracefully stops the payment handler and closes all connections.
func (p *PaymentHandler) Shutdown() {
	p.paymentProcessorClient.Shutdown()
	p.paymentProcessorFallbackClient.Shutdown()

	close(p.shutdown)
}

func (p *PaymentHandler) processPaymentAsync() {
	ctx := context.Background()

	for {
		select {
		case request := <-p.pendingPaymentChan:
			client := p.selectBestClient()

			err := client.ProcessPayment(request)
			if err != nil {
				p.logger.InfoContext(ctx, "error on pending request, trying to put on channel again")

				go func() {
					_ = p.ProcessPayment(ctx, request)
				}()
			}
		case <-p.shutdown:
			return
		}
	}
}

func (p *PaymentHandler) selectBestClient() *PaymentClient {
	return p.paymentProcessorClient
}
