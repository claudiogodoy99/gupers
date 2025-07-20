package pkg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
	"go.uber.org/zap"
)

type PaymentClient interface {
	ProcessPayment(logger zap.SugaredLogger, request *PaymentRequest) error
	GetStatus() PaymentClientState
	Shutdown()
}

type paymentClient struct {
	httpClient *http.Client
	state      PaymentClientState
	shutdown   chan struct{}

	mu             sync.RWMutex
	url            string
	healthCheckURL string
}

type PaymentClientState struct {
	histogram *hdrhistogram.Histogram
	available bool
}

func NewPaymentClient(httpClient *http.Client, url, healthCheckUrl string) PaymentClient {
	client := &paymentClient{
		httpClient: httpClient,
		state: PaymentClientState{available: true,
			histogram: hdrhistogram.New(1, int64(time.Minute.Microseconds()), 3)},
		shutdown:       make(chan struct{}),
		url:            url,
		healthCheckURL: healthCheckUrl,
	}

	go client.monitorClient()
	return client
}

func (c *paymentClient) monitorClient() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.performHealthCheck()
		case <-c.shutdown:
			return
		}
	}
}

func (c *paymentClient) update(available bool, timeTaken time.Duration) {
	// Sorry Teoi I swear I tried to use channel to control the state
	c.mu.Lock()
	c.state.available = available
	if timeTaken > 0 {
		c.state.histogram.RecordValue(timeTaken.Microseconds())
	}
	c.mu.Unlock()
}

func (c *paymentClient) performHealthCheck() {
	// TODO: verify if the health check time is going to be correlated with response time,
	// if not we should not track time here
	since := time.Now()
	var err error
	defer func() {
		timeTaken := time.Since(since)
		c.update(err == nil, timeTaken)
	}()

	resp, err := c.httpClient.Get(c.healthCheckURL)
	if err != nil {
		return
	}
	defer resp.Body.Close()

}

type stateRequest struct {
	available bool
	duration  time.Duration
}

func (c *paymentClient) ProcessPayment(logger zap.SugaredLogger, request *PaymentRequest) error {
	since := time.Now()
	var err error
	defer func() {
		timeTaken := time.Since(since)
		c.update(err == nil, timeTaken)
	}()

	jsonData, err := json.Marshal(request)
	if err != nil {
		logger.Errorf("failed to marshal request: %w", err)
		return err
	}

	resp, err := c.httpClient.Post(c.url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		logger.Errorf("failed to send payment request: %w", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = fmt.Errorf("payment processor returned status: %d", resp.StatusCode)
		return err
	}

	return nil
}

// TODO: vai dar data race, pensar em como protect usando canal
func (c *paymentClient) GetStatus() PaymentClientState {
	c.mu.RLock()
	localCopy := c.state
	c.mu.RUnlock()
	return localCopy
}

func (c *paymentClient) Shutdown() {
	close(c.shutdown)
}
