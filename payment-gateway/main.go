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

func createPaymentClients(ctx context.Context,
	clients httpClients,
	config configuration,
	logger *slog.Logger,
) paymentClients {
	return paymentClients{
		primary:  internal.NewPaymentClient(ctx, clients.primary, config.primaryURL, config.primaryHealthURL, logger),
		fallback: internal.NewPaymentClient(ctx, clients.fallback, config.fallbackURL, config.fallbackHealthURL, logger),
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

func NewServer() *Server { // function length acceptable due to setup logic
	ctx := context.Background()

	port := getEnvDefault("PORT", "8080")
	dbURL := mustGetEnv("DATABASE_URL")
	logger := newLogger()
	dbClient := internal.NewPostgresDBClient(ctx, dbURL)
	config := loadConfiguration()
	logConfiguration(ctx, logger, config)

	mux := http.NewServeMux()
	paymentHandler := buildPaymentHandler(ctx, config, logger, dbClient)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  serverReadTimeout * time.Second,
		WriteTimeout: serverWriteTimeout * time.Second,
		IdleTimeout:  serverIdleTimeout * time.Second,
	}

	return &Server{port: port, mux: mux, server: server, handler: paymentHandler, logger: logger}
}

func newLogger() *slog.Logger { return slog.New(slog.NewTextHandler(os.Stdout, nil)) }

func getEnvDefault(key, def string) string {
	val := os.Getenv(key)
	if val != "" {
		return val
	}

	return def
}

func mustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(key + " not set")
	}

	return v
}

func logConfiguration(ctx context.Context, logger *slog.Logger, cfg configuration) {
	logger.InfoContext(ctx, "Payment processor configuration",
		slog.String("primary_url", cfg.primaryURL),
		slog.String("primary_health_url", cfg.primaryHealthURL),
		slog.String("fallback_url", cfg.fallbackURL),
		slog.String("fallback_health_url", cfg.fallbackHealthURL),
	)
}

func buildPaymentHandler(
	ctx context.Context,
	cfg configuration,
	logger *slog.Logger,
	dbClient internal.DBClient,
) *internal.PaymentHandler {
	clients := createHTTPClients()
	paymentClients := createPaymentClients(ctx, clients, cfg, logger)
	workerConf := loadWorkerConfiguration(ctx, logger)

	logger.InfoContext(ctx, "Payment handler configuration",
		slog.Int("channel_len", workerConf.channelLen),
		slog.Int("num_workers", workerConf.numWorkers),
	)

	channel := make(chan *internal.PaymentRequest, workerConf.channelLen)
	router := internal.NewRouter(
		cfg.routerThreshold,
		workerConf.channelLen,
		channel,
		paymentClients.primary,
		paymentClients.fallback,
	)

	return internal.NewPaymentHandler(ctx, router, workerConf.numWorkers, channel, logger, dbClient)
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

func (s *Server) paymentsHandler(writer http.ResponseWriter, request *http.Request) { // route handler
	if request.Method != http.MethodPost {
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)

		return
	}

	ctx := request.Context()
	s.logger.DebugContext(ctx, "Payment request received")

	var req internal.PaymentRequest

	decodeErr := json.NewDecoder(request.Body).Decode(&req)
	if decodeErr != nil {
		s.errorJSON(ctx, writer, http.StatusBadRequest, "Invalid request body", "Failed to decode JSON request")

		return
	}

	if req.RequestedAt.IsZero() {
		req.RequestedAt = time.Now().UTC()
	}

	processErr := s.handler.ProcessPayment(ctx, &req)
	if processErr != nil {
		s.errorJSON(ctx, writer, http.StatusInternalServerError, "Failed to process payment", "Payment processing failed")

		return
	}

	s.respondJSON(ctx, writer, http.StatusOK, map[string]string{"status": "processed"})
}

func (s *Server) setupRoutes() {
	s.mux.HandleFunc("/payments", s.paymentsHandler)
	s.mux.HandleFunc("/payments-summary", s.paymentsSummaryHandler)
}

func (s *Server) paymentsSummaryHandler(writer http.ResponseWriter, request *http.Request) { // route handler
	if request.Method != http.MethodGet {
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)

		return
	}

	fromTime, toTime, err := parseTimeRange(request.URL.Query().Get("from"), request.URL.Query().Get("to"))
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)

		return
	}

	summaries, err := s.handler.GetPaymentsSummary(request.Context(), fromTime, toTime)
	if err != nil {
		s.logger.ErrorContext(request.Context(), "Failed to read payments summary", slog.String("error", err.Error()))
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

		return
	}

	defaultAmount, okDefault := summaries[0].TotalAmount.Float64()
	if !okDefault {
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

		return
	}

	fallbackAmount, okFallback := summaries[1].TotalAmount.Float64()
	if !okFallback {
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

		return
	}

	resp := map[string]any{
		"default": map[string]any{
			"totalRequests": summaries[0].TotalRequests,
			"totalAmount":   defaultAmount,
		},
		"fallback": map[string]any{
			"totalRequests": summaries[1].TotalRequests,
			"totalAmount":   fallbackAmount,
		},
	}

	s.respondJSON(request.Context(), writer, http.StatusOK, resp)
}

func parseTimeRange(fromStr, toStr string) (time.Time, time.Time, error) {
	var from time.Time
	if fromStr != "" {
		parsedFrom, parseErr := time.Parse(time.RFC3339, fromStr)
		if parseErr != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid from parameter: %w", parseErr)
		}

		from = parsedFrom
	}

	toTime := time.Now()
	if toStr != "" {
		parsedTo, parseErr := time.Parse(time.RFC3339, toStr)
		if parseErr != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid to parameter: %w", parseErr)
		}

		toTime = parsedTo
	}

	return from, toTime, nil
}

func (s *Server) respondJSON(ctx context.Context, writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)

	encodeErr := json.NewEncoder(writer).Encode(payload)
	if encodeErr != nil {
		s.logger.ErrorContext(ctx, "Failed to encode JSON response", slog.String("error", encodeErr.Error()))
	}
}

func (s *Server) errorJSON(ctx context.Context, writer http.ResponseWriter, status int, errTitle, message string) {
	s.respondJSON(ctx, writer, status, ErrorResponse{Error: errTitle, Message: message})
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
