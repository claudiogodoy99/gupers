package internal

// Router handles payment processing and fallback logic.
type Router struct {
	paymentProcessorClient         *PaymentClient
	paymentProcessorFallbackClient *PaymentClient
	threshold                      int
	pendingPaymentChanSlice        [4]chan []byte
	bufSize                        int
}

// NewRouter creates a new Router instance.
func NewRouter(threshold, bufSize int, pendingPaymentChanSlice [4]chan []byte,
	paymentProcessorClient, paymentProcessorFallbackClient *PaymentClient,
) *Router {
	return &Router{
		paymentProcessorClient:         paymentProcessorClient,
		paymentProcessorFallbackClient: paymentProcessorFallbackClient,
		threshold:                      threshold,
		bufSize:                        bufSize,
		pendingPaymentChanSlice:        pendingPaymentChanSlice,
	}
}

// Shutdown gracefully shuts down the Router.
func (r *Router) Shutdown() {
	r.paymentProcessorClient.Shutdown()
	r.paymentProcessorFallbackClient.Shutdown()
}

func (r *Router) route() *PaymentClient {
	const percentMultiplier = 100

	sumLen := 0
	for i := range len(r.pendingPaymentChanSlice) {
		sumLen += len(r.pendingPaymentChanSlice[i])
	}

	channelUsage := sumLen * percentMultiplier / r.bufSize

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

		return r.paymentProcessorClient
	}

	return r.paymentProcessorClient
}
