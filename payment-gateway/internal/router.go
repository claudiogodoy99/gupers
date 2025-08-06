package internal

import "time"

// Router handles payment processing and fallback logic.
type Router struct {
	paymentProcessorClient         *PaymentClient
	paymentProcessorFallbackClient *PaymentClient
	threshold                      int
	pendingPaymentChan             chan *PaymentRequest
	bufSize                        int
}

// NewRouter creates a new Router instance.
func NewRouter(threshold, bufSize int, pendingPaymentChan chan *PaymentRequest,
	paymentProcessorClient, paymentProcessorFallbackClient *PaymentClient,
) *Router {
	return &Router{
		paymentProcessorClient:         paymentProcessorClient,
		paymentProcessorFallbackClient: paymentProcessorFallbackClient,
		threshold:                      threshold,
		bufSize:                        bufSize,
		pendingPaymentChan:             pendingPaymentChan,
	}
}

// Shutdown gracefully shuts down the Router.
func (r *Router) Shutdown() {
	r.paymentProcessorClient.Shutdown()
	r.paymentProcessorFallbackClient.Shutdown()
}

func (r *Router) route() *PaymentClient {
	const percentMultiplier = 100

	const fallbackSleepDuration = 500 * time.Millisecond

	channelUsage := len(r.pendingPaymentChan) * percentMultiplier / r.bufSize

	if r.paymentProcessorClient.health.Load() || channelUsage <= r.threshold {
		return r.paymentProcessorClient
	}

	if !r.paymentProcessorClient.health.Load() {
		if r.paymentProcessorFallbackClient.health.Load() {
			if r.paymentProcessorFallbackClient.count.Load()%5 == 0 {
				return r.paymentProcessorClient
			}

			return r.paymentProcessorFallbackClient
		}

		time.Sleep(fallbackSleepDuration)

		return r.paymentProcessorClient
	}

	return r.paymentProcessorClient
}
