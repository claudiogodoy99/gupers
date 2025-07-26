package internal

import (
	"time"

	"go.uber.org/zap"
)

type PaymentHandler struct {
	paymentProcessorClient         PaymentClient
	paymentProcessorFallbackClient PaymentClient
	serverLogger                   *zap.SugaredLogger
	pendingPaymentChan             chan *PendingRequest
	shutdown                       chan struct{}
}

func NewPaymentHandler(paymentProcessorClient, paymentProcessorFallbackClient PaymentClient, chanLen, numWorkers int, logger *zap.SugaredLogger) *PaymentHandler {
	payment := &PaymentHandler{
		paymentProcessorClient:         paymentProcessorClient,
		paymentProcessorFallbackClient: paymentProcessorFallbackClient,
		serverLogger:                   logger,
		pendingPaymentChan:             make(chan *PendingRequest, chanLen),
		shutdown:                       make(chan struct{}),
	}

	for range numWorkers {
		go payment.processPaymentAsync()
	}

	return payment
}

type PendingRequest struct {
	request *PaymentRequest
	logger  *zap.SugaredLogger
}

type PaymentRequest struct {
	Amount        float64
	CorrelationID string
	RequestedAt   time.Time
}

func (p *PaymentHandler) ProcessPayment(logger *zap.SugaredLogger, request *PaymentRequest) error {
	logger.Debugf("received request, adding into channel - channel len (%d)", len(p.pendingPaymentChan))

	req := &PendingRequest{
		request: request,
		logger:  logger,
	}

	ticker := time.Tick(100 * time.Millisecond)

	for {
		select {
		case p.pendingPaymentChan <- req:
			return nil
		case <-ticker:
			logger.Warnf("taking too long to add request on channel - channel len (%d)", len(p.pendingPaymentChan))
		case <-p.shutdown:
			return nil
		}
	}
}

func (p *PaymentHandler) processPaymentAsync() {
	for {
		select {
		case pending := <-p.pendingPaymentChan:
			client := p.selectBestClient()

			err := client.ProcessPayment(*pending.logger, pending.request)
			if err != nil {
				pending.logger.Info("error on pending request, trying to put on channel again")
				go p.ProcessPayment(pending.logger, pending.request)
			}
			//TODO db integration
		case <-p.shutdown:
			return
		}
	}
}

// TODO
func (p *PaymentHandler) selectBestClient() PaymentClient {
	return p.paymentProcessorClient
}

func (c *PaymentHandler) Shutdown() {
	c.paymentProcessorClient.Shutdown()
	c.paymentProcessorFallbackClient.Shutdown()

	close(c.shutdown)
}
