package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/claudiogodoy/gupers/payment-gateway/internal"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Server struct {
	port    string
	mux     *http.ServeMux
	server  *http.Server
	handler *internal.PaymentHandler
	logger  *zap.Logger
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func NewServer() *Server {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Get payment processor URLs from environment variables
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

	// Initialize Zap logger with configuration
	loggerConfig := NewLoggerConfig()
	logger, err := InitLogger(loggerConfig)

	if err != nil {
		panic("Failed to initialize logger: " + err.Error())
	}

	// Log the configured URLs
	logger.Info("Payment processor configuration",
		zap.String("primary_url", primaryURL),
		zap.String("primary_health_url", primaryHealthURL),
		zap.String("fallback_url", fallbackURL),
		zap.String("fallback_health_url", fallbackHealthURL),
	)

	// Create HTTP mux
	mux := http.NewServeMux()

	// Create HTTP clients with timeouts
	primaryHTTPClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	fallbackHTTPClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create payment clients
	primaryPaymentClient := internal.NewPaymentClient(primaryHTTPClient, primaryURL, primaryHealthURL, logger.Sugar())
	fallbackPaymentClient := internal.NewPaymentClient(fallbackHTTPClient, fallbackURL, fallbackHealthURL, logger.Sugar())

	// Get channel length and number of workers from environment variables
	channelLen := 10 // default value
	if channelLenStr := os.Getenv("PAYMENT_CHANNEL_LEN"); channelLenStr != "" {
		if val, err := strconv.Atoi(channelLenStr); err == nil {
			channelLen = val
		} else {
			logger.Warn("Invalid PAYMENT_CHANNEL_LEN, using default", zap.String("value", channelLenStr), zap.Int("default", channelLen))
		}
	}

	numWorkers := 10 // default value
	if numWorkersStr := os.Getenv("PAYMENT_NUM_WORKERS"); numWorkersStr != "" {
		if val, err := strconv.Atoi(numWorkersStr); err == nil {
			numWorkers = val
		} else {
			logger.Warn("Invalid PAYMENT_NUM_WORKERS, using default", zap.String("value", numWorkersStr), zap.Int("default", numWorkers))
		}
	}

	logger.Info("Payment handler configuration",
		zap.Int("channel_len", channelLen),
		zap.Int("num_workers", numWorkers),
	)

	// Create payment handler with both clients
	paymentHandler := internal.NewPaymentHandler(primaryPaymentClient, fallbackPaymentClient, channelLen, numWorkers, logger.Sugar())

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &Server{
		port:    port,
		mux:     mux,
		server:  server,
		handler: paymentHandler,
		logger:  logger,
	}
}

func (s *Server) paymentsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}

	s.logger.Debug("Payment request received")

	var paymentRequest internal.PaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&paymentRequest); err != nil {
		s.logger.Error("error on decode json", zap.Error(err))

		response := ErrorResponse{
			Error:   "Invalid request body",
			Message: "Failed to decode JSON request",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(response)
		return
	}

	scopedLogger := s.logger.With(zapcore.Field{
		Key:    "CorrelationId",
		Type:   zapcore.StringType,
		String: paymentRequest.CorrelationID,
	}).Sugar()

	scopedLogger.Infof("processing payment: %s", paymentRequest)
	err := s.handler.ProcessPayment(scopedLogger, &paymentRequest)
	if err != nil {
		scopedLogger.Errorf("Failed to process payment: %v", err)

		response := ErrorResponse{
			Error:   "Failed to process payment",
			Message: "Payment processing failed",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(response)
		return
	}

	scopedLogger.Debug("Payment processed successfully")

	response := map[string]string{"status": "processed"}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (s *Server) setupRoutes() {
	s.mux.HandleFunc("/payments", s.paymentsHandler)
}

func (s *Server) Start() error {
	s.setupRoutes()

	s.logger.Info("Starting server",
		zap.String("port", s.port),
		zap.String("health_endpoint", "http://localhost:"+s.port+"/health"),
		zap.String("payments_endpoint", "http://localhost:"+s.port+"/payments"),
	)

	return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down server gracefully")
	s.handler.Shutdown()

	// Shutdown the HTTP server with context
	if err := s.server.Shutdown(ctx); err != nil {
		return err
	}

	return s.logger.Sync()
}

func main() {
	server := NewServer()

	// Setup graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			server.logger.Fatal("Server failed to start", zap.Error(err))
		}
	}()

	server.logger.Info("Server started successfully. Press Ctrl+C to shutdown.")

	<-c

	server.logger.Info("Shutdown signal received")

	// Create a deadline for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown server
	if err := server.Shutdown(ctx); err != nil {
		server.logger.Error("Error during shutdown", zap.Error(err))
	}

	<-ctx.Done()
	server.logger.Info("Shutdown completed")
}
