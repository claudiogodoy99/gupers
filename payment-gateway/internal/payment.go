package internal

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	decimal "github.com/shopspring/decimal"
)

const (
	tickerInterval = 100
)

// ErrDBClientUnavailable is returned when a database client hasn't been configured.
var ErrDBClientUnavailable = errors.New("database client not available")

// PaymentHandler manages payment processing with primary and fallback clients
// using a fan-in/fan-out pattern for latency.
type PaymentHandler struct {
	router                  *Router
	logger                  *slog.Logger
	pendingPaymentChanSlice [4]chan []byte
	shutdown                chan struct{}
	dbClient                DBClient
}

// NewPaymentHandler creates a new payment handler with the specified clients and worker configuration.
// It starts the specified number of workers to process payments asynchronously.
func NewPaymentHandler(ctx context.Context,
	router *Router,
	numWorkers int,
	pendingPaymentChanSlice [4]chan []byte,
	logger *slog.Logger,
	dbClient DBClient,
) *PaymentHandler {
	payment := &PaymentHandler{
		router:                  router,
		logger:                  logger,
		pendingPaymentChanSlice: pendingPaymentChanSlice,
		shutdown:                make(chan struct{}),
		dbClient:                dbClient,
	}

	for range numWorkers {
		go payment.processPaymentWorker(ctx)
	}

	return payment
}

// PaymentRequest represents a payment processing request with amount, correlation ID, and timestamp.
type PaymentRequest struct {
	Amount        decimal.Decimal `json:"amount"`
	CorrelationID string          `json:"correlationId"`
	RequestedAt   time.Time       `json:"requestedAt"`
	TypeClient    PaymentType     `json:"type,omitempty"`
}

// PaymentType identifies which processor handled the payment.
type PaymentType int

// PaymentTypeDefault indicates the default payment processor was used.
const (
	PaymentTypeDefault PaymentType = iota
	PaymentTypeFallback
)

// ProcessPayment adds a payment request to the pending channel for processing.
func (p *PaymentHandler) ProcessPayment(ctx context.Context, request []byte) error {
	ticker := time.NewTicker(tickerInterval * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case p.pendingPaymentChanSlice[0] <- request:
			return nil
		case p.pendingPaymentChanSlice[1] <- request:
			return nil
		case p.pendingPaymentChanSlice[2] <- request:
			return nil
		case p.pendingPaymentChanSlice[3] <- request:
			return nil
		case <-ticker.C:
			// p.logger.WarnContext(ctx, "taking too long to add request on channel",
			// 	slog.Int("channel_len", len(p.pendingPaymentChan)))
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
		case data := <-p.pendingPaymentChanSlice[0]:
			p.process(ctx, data)
		case data := <-p.pendingPaymentChanSlice[1]:
			p.process(ctx, data)
		case data := <-p.pendingPaymentChanSlice[2]:
			p.process(ctx, data)
		case data := <-p.pendingPaymentChanSlice[3]:
			p.process(ctx, data)
		case <-p.shutdown:
			return
		}
	}
}

func (p *PaymentHandler) process(ctx context.Context, data []byte) {
	var request *PaymentRequest

	err := json.NewDecoder(bytes.NewReader(data)).Decode(&request)
	if err != nil {
		p.logger.ErrorContext(ctx, fmt.Sprintf("error decoding json body %v", err))
	}

	routeClientAndSendRequest := func() error {
		client := p.router.route()

		if client == p.router.paymentProcessorClient {
			request.TypeClient = PaymentTypeDefault
		} else {
			request.TypeClient = PaymentTypeFallback
		}

		p.logger.InfoContext(ctx, "Processing payment",
			slog.String("correlationId", request.CorrelationID),
			slog.String("amount", request.Amount.String()))

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
		initialBackoff = 10   // 10 milliseconds
		maxBackoff     = 5000 // 5 seconds
	)

	backoff := initialBackoff
	bigBackoff := big.NewInt(int64(backoff))

	for {
		err := action()
		if err == nil {
			return
		}

		sleep, err := rand.Int(rand.Reader, bigBackoff)
		if err != nil {
			continue
		}

		time.Sleep(time.Duration(sleep.Int64()) * time.Millisecond)

		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}

		bigBackoff.SetInt64(int64(backoff))
	}
}
