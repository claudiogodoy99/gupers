package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/claudiogodoy/gupers/go-server/pkg"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Server struct {
	port    string
	router  *gin.Engine
	handler *pkg.PaymentHandler
	logger  *zap.Logger
}

type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
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

	// Set Gin mode based on environment
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()

	// Add Zap middleware instead of default Gin logger
	router.Use(ginzap.Ginzap(logger, time.RFC3339, true))
	router.Use(ginzap.RecoveryWithZap(logger, true))

	// Create HTTP clients with timeouts
	primaryHTTPClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	fallbackHTTPClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create payment clients
	primaryPaymentClient := pkg.NewPaymentClient(primaryHTTPClient, primaryURL, primaryHealthURL)
	fallbackPaymentClient := pkg.NewPaymentClient(fallbackHTTPClient, fallbackURL, fallbackHealthURL)

	// Create payment handler with both clients
	paymentHandler := pkg.NewPaymentHandler(primaryPaymentClient, fallbackPaymentClient)

	return &Server{
		port:    port,
		router:  router,
		handler: paymentHandler,
		logger:  logger,
	}
}

func (s *Server) healthHandler(c *gin.Context) {
	s.logger.Info("Health check endpoint accessed",
		zap.String("client_ip", c.ClientIP()),
		zap.String("user_agent", c.GetHeader("User-Agent")),
	)

	response := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now(),
		Message:   "Go web server is running",
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) notFoundHandler(c *gin.Context) {
	s.logger.Warn("Route not found",
		zap.String("path", c.Request.URL.Path),
		zap.String("method", c.Request.Method),
		zap.String("client_ip", c.ClientIP()),
	)

	response := ErrorResponse{
		Error:   "Not Found",
		Message: "The requested path '" + c.Request.URL.Path + "' was not found",
	}

	c.JSON(http.StatusNotFound, response)
}

func (s *Server) methodNotAllowedHandler(c *gin.Context) {
	s.logger.Warn("Method not allowed",
		zap.String("path", c.Request.URL.Path),
		zap.String("method", c.Request.Method),
		zap.String("client_ip", c.ClientIP()),
	)

	response := ErrorResponse{
		Error:   "Method Not Allowed",
		Message: "Method '" + c.Request.Method + "' is not allowed for this endpoint",
	}

	c.JSON(http.StatusMethodNotAllowed, response)
}

func (s *Server) setupRoutes() {
	// Handle 404 errors
	s.router.NoRoute(s.notFoundHandler)
	s.router.NoMethod(s.methodNotAllowedHandler)

	s.router.GET("/health", s.healthHandler)
	s.router.POST("/payments", func(c *gin.Context) {
		s.logger.Debug("Payment request received",
			zap.String("client_ip", c.ClientIP()),
			zap.String("user_agent", c.GetHeader("User-Agent")),
		)

		var paymentRequest pkg.PaymentRequest
		if err := c.ShouldBindJSON(&paymentRequest); err != nil {
			s.logger.Error("error on decode json", zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}

		scopedLogger := s.logger.With(zapcore.Field{
			Key:    "correlation_id",
			Type:   zapcore.StringType,
			String: paymentRequest.CorrelationID,
		}).Sugar()

		err := s.handler.ProcessPayment(*scopedLogger, &paymentRequest)
		if err != nil {
			scopedLogger.Errorf("Failed to process payment %w", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process payment"})
			return
		}

		scopedLogger.Debug("Payment processed successfully")
		c.JSON(http.StatusOK, gin.H{"status": "processed"})
	})
}

func (s *Server) Start() error {
	s.setupRoutes()

	s.logger.Info("Starting server",
		zap.String("port", s.port),
		zap.String("health_endpoint", "http://localhost:"+s.port+"/health"),
		zap.String("payments_endpoint", "http://localhost:"+s.port+"/payments"),
	)

	return s.router.Run(":" + s.port)
}

func (s *Server) Shutdown() error {
	s.logger.Info("Shutting down server gracefully")
	s.handler.Shutdown()
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
	if err := server.Shutdown(); err != nil {
		server.logger.Error("Error during shutdown", zap.Error(err))
	}

	select {
	case <-ctx.Done():
		server.logger.Info("Shutdown completed")
	}
}
