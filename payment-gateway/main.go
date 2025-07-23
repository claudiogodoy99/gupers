package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
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
	primaryPaymentClient := internal.NewPaymentClient(primaryHTTPClient, primaryURL, primaryHealthURL)
	fallbackPaymentClient := internal.NewPaymentClient(fallbackHTTPClient, fallbackURL, fallbackHealthURL)

	// Create payment handler with both clients
	paymentHandler := internal.NewPaymentHandler(primaryPaymentClient, fallbackPaymentClient)

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

func (s *Server) notFoundHandler(w http.ResponseWriter, r *http.Request) {
	s.logger.Warn("Route not found",
		zap.String("path", r.URL.Path),
		zap.String("method", r.Method),
	)

	response := ErrorResponse{
		Error:   "Not Found",
		Message: "The requested path '" + r.URL.Path + "' was not found",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(response)
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
		Key:    "correlation_id",
		Type:   zapcore.StringType,
		String: paymentRequest.CorrelationID,
	}).Sugar()

	err := s.handler.ProcessPayment(*scopedLogger, &paymentRequest)
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
	s.mux.HandleFunc("/payments", s.withMiddleware(s.paymentsHandler))

	// Handle all other routes as 404
	s.mux.HandleFunc("/", s.withMiddleware(s.notFoundHandler))
}

// Middleware wrapper for logging and recovery
func (s *Server) withMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Recovery middleware
		defer func() {
			if err := recover(); err != nil {
				s.logger.Error("Panic recovered",
					zap.Any("error", err),
					zap.String("path", r.URL.Path),
					zap.String("method", r.Method),
				)

				response := ErrorResponse{
					Error:   "Internal Server Error",
					Message: "An unexpected error occurred",
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(response)
			}
		}()

		// Call the next handler
		next(w, r)

		// Log request
		duration := time.Since(start)
		s.logger.Info("Request completed",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Duration("duration", duration),
		)
	}
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
