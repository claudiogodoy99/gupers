package internal

import (
	"fmt"
	"time"

	"go.uber.org/zap"
)

type PaymentHandler struct {
	paymentProcessorClient         PaymentClient
	paymentProcessorFallbackClient PaymentClient
	serverLogger                   *zap.SugaredLogger
}

func NewPaymentHandler(paymentProcessorClient, paymentProcessorFallbackClient PaymentClient, logger *zap.SugaredLogger) *PaymentHandler {
	return &PaymentHandler{
		paymentProcessorClient:         paymentProcessorClient,
		paymentProcessorFallbackClient: paymentProcessorFallbackClient,
		serverLogger:                   logger,
	}
}

type PaymentRequest struct {
	Amount        float64
	CorrelationID string
	RequestedAt   time.Time
}

// POC Logic
func (p *PaymentHandler) ProcessPayment(logger zap.SugaredLogger, request *PaymentRequest) error {
	primaryStatus := p.paymentProcessorClient.GetStatus()
	if primaryStatus.available {
		err := p.paymentProcessorClient.ProcessPayment(logger, request)
		if err == nil {
			return nil
		}
	} else {
		logger.Warn("Primary payment processor unavailable, using fallback")
	}

	// Try fallback client
	fallbackStatus := p.paymentProcessorFallbackClient.GetStatus()
	if !fallbackStatus.available {
		return fmt.Errorf("both primary and fallback payment processors are unavailable")
	}

	err := p.paymentProcessorFallbackClient.ProcessPayment(logger, request)
	if err != nil {
		return fmt.Errorf("fallback payment processor failed: %w", err)
	}

	return nil
}

func (c *PaymentHandler) Shutdown() {
	c.paymentProcessorClient.Shutdown()
	c.paymentProcessorFallbackClient.Shutdown()
}
