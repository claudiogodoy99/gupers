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
	paymentProcessorClient         PaymentClient
	paymentProcessorFallbackClient PaymentClient
	dbClient                       DBClient
	logger                         *slog.Logger
	pendingPaymentChan             chan *PaymentRequest
	shutdown                       chan struct{}
}

// NewPaymentHandler creates a new payment handler with the specified clients and worker configuration.
// It starts the specified number of workers to process payments asynchronously.
func NewPaymentHandler(
	paymentProcessorClient, paymentProcessorFallbackClient PaymentClient,
	dbClient DBClient,
	chanLen, numWorkers int,
	logger *slog.Logger,
) *PaymentHandler {
	payment := &PaymentHandler{
		paymentProcessorClient:         paymentProcessorClient,
		paymentProcessorFallbackClient: paymentProcessorFallbackClient,
		dbClient:                       dbClient,
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
func (p *PaymentHandler) ProcessPayment(request *PaymentRequest) error {
	ctx := context.Background()
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
					_ = p.ProcessPayment(request)
				}()
			} else {
				// Save to database after successful payment processing
				if p.dbClient != nil {
					dbErr := p.dbClient.Write(*request)
					if dbErr != nil {
						p.logger.ErrorContext(ctx, "Failed to save payment to database",
							slog.String("correlation_id", request.CorrelationID),
							slog.String("error", dbErr.Error()))
					} else {
						p.logger.DebugContext(ctx, "Payment saved to database successfully",
							slog.String("correlation_id", request.CorrelationID))
					}
				} else {
					p.logger.WarnContext(ctx, "Database client is nil, payment not saved to database",
						slog.String("correlation_id", request.CorrelationID))
				}
			}
		case <-p.shutdown:
			return
		}
	}
}

//nolint:ireturn // Interface return is intentional for polymorphism
func (p *PaymentHandler) selectBestClient() PaymentClient {
	return p.paymentProcessorClient
}
