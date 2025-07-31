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
	port     string
	mux      *http.ServeMux
	server   *http.Server
	handler  *internal.PaymentHandler
	dbClient internal.DBClient
	logger   *zap.Logger
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

	// Create payment handler with both clients
	paymentHandler := internal.NewPaymentHandler(primaryPaymentClient, fallbackPaymentClient, logger.Sugar())

	// Initialize database client
	dbConnectionString := os.Getenv("DATABASE_URL")
	if dbConnectionString == "" {
		dbConnectionString = "postgres://postgres:postgres@localhost:5432/rinha?sslmode=disable"
	}

	dbClient, err := internal.NewPostgresDBClient(dbConnectionString)
	if err != nil {
		logger.Fatal("Failed to initialize database client", zap.Error(err))
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &Server{
		port:     port,
		mux:      mux,
		server:   server,
		handler:  paymentHandler,
		dbClient: dbClient,
		logger:   logger,
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

	// Set the requested time if not provided
	if paymentRequest.RequestedAt.IsZero() {
		paymentRequest.RequestedAt = time.Now()
	}

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

	// Save payment to database after successful processing
	if err := s.dbClient.Write(paymentRequest); err != nil {
		scopedLogger.Errorf("Failed to save payment to database: %v", err)
	} else {
		scopedLogger.Debug("Payment saved to database successfully")
	}

	scopedLogger.Debug("Payment processed successfully")

	response := map[string]string{"status": "processed"}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (s *Server) dbHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	s.logger.Debug("Database summary request received")

	// Parse query parameters for time range
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")

	if fromStr == "" || toStr == "" {
		response := ErrorResponse{
			Error:   "Missing query parameters",
			Message: "Both 'from' and 'to' query parameters are required",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Parse time strings (simple format: 2006-01-02 15:04:05)
	fromTime, err := time.Parse("2006-01-02 15:04:05", fromStr)
	if err != nil {
		response := ErrorResponse{
			Error:   "Invalid time format",
			Message: "Parameter 'from' must be in format: 2006-01-02 15:04:05 (e.g., 2020-07-10 12:34:56)",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(response)
		return
	}

	toTime, err := time.Parse("2006-01-02 15:04:05", toStr)
	if err != nil {
		response := ErrorResponse{
			Error:   "Invalid time format",
			Message: "Parameter 'to' must be in format: 2006-01-02 15:04:05 (e.g., 2020-07-10 12:35:56)",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Get summary data from database
	summaries := s.dbClient.Read(fromTime, toTime)

	// Create response in the expected format
	response := map[string]interface{}{
		"default": map[string]interface{}{
			"totalRequests": summaries[internal.Default].TotalRequests,
			"totalAmount":   summaries[internal.Default].TotalAmount,
		},
		"fallback": map[string]interface{}{
			"totalRequests": summaries[internal.Fallback].TotalRequests,
			"totalAmount":   summaries[internal.Fallback].TotalAmount,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)

	s.logger.Debug("Database summary response sent successfully")
}

func (s *Server) setupRoutes() {
	s.mux.HandleFunc("/payments", s.paymentsHandler)
	s.mux.HandleFunc("/db", s.dbHandler)
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

	// Close database connection
	if err := s.dbClient.Close(); err != nil {
		s.logger.Error("Failed to close database connection", zap.Error(err))
	}

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
