package internal

import (
	"context"
	"fmt"
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
	dbClient                       DBClient
}

// NewPaymentHandler creates a new payment handler with the specified clients and worker configuration.
// It starts the specified number of workers to process payments asynchronously.
func NewPaymentHandler(
	paymentProcessorClient, paymentProcessorFallbackClient *PaymentClient,
	chanLen, numWorkers int,
	logger *slog.Logger,
	dbClient DBClient,
) *PaymentHandler {
	payment := &PaymentHandler{
		paymentProcessorClient:         paymentProcessorClient,
		paymentProcessorFallbackClient: paymentProcessorFallbackClient,
		logger:                         logger,
		pendingPaymentChan:             make(chan *PaymentRequest, chanLen),
		shutdown:                       make(chan struct{}),
		dbClient:                       dbClient,
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

			p.logger.InfoContext(ctx, "Processing payment",
				slog.String("correlationId", request.CorrelationID),
				slog.Float64("amount", request.Amount))

			err := client.ProcessPayment(request)

			if err == nil {
				if p.dbClient != nil {
					if dbErr := p.dbClient.Write(*request); dbErr != nil {
						p.logger.ErrorContext(ctx, "failed to write payment request to database",
							slog.String("correlationId", request.CorrelationID),
							slog.String("error", dbErr.Error()))
					} else {
						p.logger.InfoContext(ctx, "Payment saved to database successfully",
							slog.String("correlationId", request.CorrelationID))
					}
				} else {
					p.logger.WarnContext(ctx, "Database client not available, payment not persisted",
						slog.String("correlationId", request.CorrelationID))
				}
			}

			if err != nil {
				p.logger.ErrorContext(ctx, "Payment processor failed",
					slog.String("correlationId", request.CorrelationID),
					slog.String("error", err.Error()))
			} else {
				p.logger.InfoContext(ctx, "Payment processor succeeded",
					slog.String("correlationId", request.CorrelationID))
			}
		case <-p.shutdown:
			return
		}
	}
}

func (p *PaymentHandler) selectBestClient() *PaymentClient {
	return p.paymentProcessorClient
}

func (p *PaymentHandler) GetPaymentsSummary(from, to time.Time) ([2]Summary, error) {
	if p.dbClient == nil {
		return [2]Summary{}, fmt.Errorf("database client not available")
	}
	return p.dbClient.Read(from, to)
}
