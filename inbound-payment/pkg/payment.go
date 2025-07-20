package pkg

import (
	"fmt"

	"go.uber.org/zap"
)

type PaymentHandler struct {
	paymentProcessorClient         PaymentClient
	paymentProcessorFallbackClient PaymentClient
}

func NewPaymentHandler(paymentProcessorClient, paymentProcessorFallbackClient PaymentClient) *PaymentHandler {
	return &PaymentHandler{
		paymentProcessorClient:         paymentProcessorClient,
		paymentProcessorFallbackClient: paymentProcessorFallbackClient,
	}
}

type PaymentRequest struct {
	Amount        float64 `json:"amount"`
	CorrelationID string  `json:"correlation_id"`
}

// POC Logic
func (p *PaymentHandler) ProcessPayment(logger zap.Logger, request *PaymentRequest) error {
	primaryStatus := p.paymentProcessorClient.GetStatus()
	if primaryStatus.available {
		err := p.paymentProcessorClient.ProcessPayment(*logger.Sugar(), request)
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

	err := p.paymentProcessorFallbackClient.ProcessPayment(*logger.Sugar(), request)
	if err != nil {
		return fmt.Errorf("fallback payment processor failed: %w", err)
	}

	return nil
}
