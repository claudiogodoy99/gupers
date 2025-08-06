// Package main implements a payment gateway service that provides high availability
// payment processing with primary and fallback payment processors.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/claudiogodoy/gupers/payment-gateway/internal"
)

const (
	httpClientTimeout  = 30
	serverReadTimeout  = 15
	serverWriteTimeout = 15
	serverIdleTimeout  = 60
	shutdownTimeout    = 30

	defaultChannelLen = 1000
	defaultNumWorkers = 28
	defaultThreshold  = 70
)

type Server struct {
	port    string
	mux     *http.ServeMux
	server  *http.Server
	handler *internal.PaymentHandler
	logger  *slog.Logger
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

type configuration struct {
	primaryURL        string
	primaryHealthURL  string
	fallbackURL       string
	fallbackHealthURL string
	routerThreshold   int
}

type httpClients struct {
	primary  *http.Client
	fallback *http.Client
}

type paymentClients struct {
	primary  *internal.PaymentClient
	fallback *internal.PaymentClient
}

type workerConfiguration struct {
	channelLen int
	numWorkers int
}

func loadConfiguration() configuration {
	primaryURL := os.Getenv("PRIMARY_PAYMENT_PROCESSOR_URL")
	if primaryURL == "" {
		primaryURL = "http://payment-processor.example.com"
	}

	primaryHealthURL := os.Getenv("PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL")
	if primaryHealthURL == "" {
		panic("PRIMARY_PAYMENT_PROCESSOR_HEALTH_URL not set")
	}

	fallbackURL := os.Getenv("FALLBACK_PAYMENT_PROCESSOR_URL")
	if fallbackURL == "" {
		panic("FALLBACK_PAYMENT_PROCESSOR_URL not set")
	}

	fallbackHealthURL := os.Getenv("FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL")
	if fallbackHealthURL == "" {
		panic("FALLBACK_PAYMENT_PROCESSOR_HEALTH_URL not set")
	}

	// Load router threshold from environment variable
	routerThreshold := defaultThreshold

	if thresholdStr := os.Getenv("ROUTER_THRESHOLD"); thresholdStr != "" {
		val, err := strconv.Atoi(thresholdStr)
		if err == nil {
			routerThreshold = val
		} else {
			log.Printf("Invalid ROUTER_THRESHOLD, using default: %v", err)
		}
	}

	return configuration{
		primaryURL:        primaryURL,
		primaryHealthURL:  primaryHealthURL,
		fallbackURL:       fallbackURL,
		fallbackHealthURL: fallbackHealthURL,
		routerThreshold:   routerThreshold,
	}
}

func createHTTPClients() httpClients {
	return httpClients{
		primary: &http.Client{
			Timeout: httpClientTimeout * time.Second,
		},
		fallback: &http.Client{
			Timeout: httpClientTimeout * time.Second,
		},
	}
}

func createPaymentClients(clients httpClients, config configuration, logger *slog.Logger) paymentClients {
	return paymentClients{
		primary:  internal.NewPaymentClient(clients.primary, config.primaryURL, config.primaryHealthURL, logger),
		fallback: internal.NewPaymentClient(clients.fallback, config.fallbackURL, config.fallbackHealthURL, logger),
	}
}

func loadWorkerConfiguration(ctx context.Context, logger *slog.Logger) workerConfiguration {
	channelLen := defaultChannelLen

	if channelLenStr := os.Getenv("PAYMENT_CHANNEL_LEN"); channelLenStr != "" {
		val, err := strconv.Atoi(channelLenStr)
		if err == nil {
			channelLen = val
		} else {
			logger.WarnContext(ctx, "Invalid PAYMENT_CHANNEL_LEN, using default",
				slog.String("value", channelLenStr), slog.Int("default", channelLen))
		}
	}

	numWorkers := defaultNumWorkers

	if numWorkersStr := os.Getenv("PAYMENT_NUM_WORKERS"); numWorkersStr != "" {
		val, err := strconv.Atoi(numWorkersStr)
		if err == nil {
			numWorkers = val
		} else {
			logger.WarnContext(ctx, "Invalid PAYMENT_NUM_WORKERS, using default",
				slog.String("value", numWorkersStr), slog.Int("default", numWorkers))
		}
	}

	return workerConfiguration{
		channelLen: channelLen,
		numWorkers: numWorkers,
	}
}

func NewServer() *Server {
	ctx := context.Background()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	config := loadConfiguration()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	logger.InfoContext(ctx, "Payment processor configuration ",
		slog.String("primary_url", config.primaryURL),
		slog.String("primary_health_url", config.primaryHealthURL),
		slog.String("fallback_url", config.fallbackURL),
		slog.String("fallback_health_url", config.fallbackHealthURL),
	)

	mux := http.NewServeMux()

	clients := createHTTPClients()
	paymentClients := createPaymentClients(clients, config, logger)
	workerConfig := loadWorkerConfiguration(ctx, logger)

	logger.InfoContext(ctx, "Payment handler configuration",
		slog.Int("channel_len", workerConfig.channelLen),
		slog.Int("num_workers", workerConfig.numWorkers),
	)

	channel := make(chan *internal.PaymentRequest, workerConfig.channelLen)
	router := internal.NewRouter(config.routerThreshold,
		workerConfig.channelLen,
		channel,
		paymentClients.primary,
		paymentClients.fallback)

	paymentHandler := internal.NewPaymentHandler(router, workerConfig.numWorkers, channel, logger)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  serverReadTimeout * time.Second,
		WriteTimeout: serverWriteTimeout * time.Second,
		IdleTimeout:  serverIdleTimeout * time.Second,
	}

	return &Server{
		port:    port,
		mux:     mux,
		server:  server,
		handler: paymentHandler,
		logger:  logger,
	}
}

func (s *Server) Start() error {
	s.setupRoutes()

	ctx := context.Background()
	s.logger.InfoContext(ctx, "Starting server",
		slog.String("port", s.port),
		slog.String("health_endpoint", "http://localhost:"+s.port+"/health"),
		slog.String("payments_endpoint", "http://localhost:"+s.port+"/payments"),
	)

	err := s.server.ListenAndServe()
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.InfoContext(ctx, "Shutting down server gracefully")
	s.handler.Shutdown()

	err := s.server.Shutdown(ctx)
	if err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	return nil
}

func (s *Server) paymentsHandler(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		return
	}

	ctx := request.Context()
	s.logger.DebugContext(ctx, "Payment request received")

	var paymentRequest internal.PaymentRequest

	err := json.NewDecoder(request.Body).Decode(&paymentRequest)
	if err != nil {
		s.logger.ErrorContext(ctx, fmt.Sprintf("error on decode json: %v", err))

		response := ErrorResponse{
			Error:   "Invalid request body",
			Message: "Failed to decode JSON request",
		}

		responseWriter.Header().Set("Content-Type", "application/json")
		responseWriter.WriteHeader(http.StatusBadRequest)

		encodeErr := json.NewEncoder(responseWriter).Encode(response)
		if encodeErr != nil {
			s.logger.ErrorContext(ctx, fmt.Sprintf("Failed to encode error response: %v", encodeErr))
		}

		return
	}

	err = s.handler.ProcessPayment(ctx, &paymentRequest)
	if err != nil {
		s.logger.ErrorContext(ctx, fmt.Sprintf("Failed to process payment: %v", err))

		response := ErrorResponse{
			Error:   "Failed to process payment",
			Message: "Payment processing failed",
		}

		responseWriter.Header().Set("Content-Type", "application/json")
		responseWriter.WriteHeader(http.StatusInternalServerError)

		encodeErr := json.NewEncoder(responseWriter).Encode(response)
		if encodeErr != nil {
			s.logger.ErrorContext(ctx, fmt.Sprintf("Failed to encode error response: %v", encodeErr))
		}

		return
	}

	s.logger.DebugContext(ctx, "Payment processed successfully")

	response := map[string]string{"status": "processed"}

	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(http.StatusOK)

	encodeErr := json.NewEncoder(responseWriter).Encode(response)
	if encodeErr != nil {
		s.logger.ErrorContext(ctx, fmt.Sprintf("Failed to encode success response: %v", encodeErr))
	}
}

func (s *Server) setupRoutes() {
	s.mux.HandleFunc("/payments", s.paymentsHandler)
}

func main() {
	ctx := context.Background()
	server := NewServer()

	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, os.Interrupt, syscall.SIGTERM)

	go func() {
		err := server.Start()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic("Server failed to start")
		}
	}()

	server.logger.InfoContext(ctx, "Server started successfully. Press Ctrl+C to shutdown.")

	<-signalChannel

	server.logger.InfoContext(ctx, "Shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout*time.Second)
	defer cancel()

	err := server.Shutdown(ctx)
	if err != nil {
		server.logger.ErrorContext(ctx, fmt.Sprintf("Error during shutdown: %v", err))
	}

	<-ctx.Done()
	server.logger.InfoContext(ctx, "Shutdown completed")
}
