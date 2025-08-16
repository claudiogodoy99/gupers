// Package main implements a payment gateway service that provides high availability
// payment processing with primary and fallback payment processors.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"syscall"
	"time"

	_ "net/http/pprof"

	"runtime/metrics"

	"github.com/claudiogodoy/gupers/payment-gateway/internal"
	"github.com/valyala/fasthttp"
)

const (
	httpClientTimeout  = 30
	serverReadTimeout  = 15
	serverWriteTimeout = 15
	serverIdleTimeout  = 120
	shutdownTimeout    = 30

	defaultChannelLen = 1000
	defaultNumWorkers = 28
	defaultThreshold  = 70
)

type Server struct {
	port    string
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

func createPaymentClients(ctx context.Context,
	config configuration,
	logger *slog.Logger,
) paymentClients {
	readTimeout, _ := time.ParseDuration("500ms")
	writeTimeout, _ := time.ParseDuration("5s")
	maxIdleConnDuration, _ := time.ParseDuration("1h")

	client := &fasthttp.Client{
		ReadTimeout:                   readTimeout,
		WriteTimeout:                  writeTimeout,
		MaxIdleConnDuration:           maxIdleConnDuration,
		NoDefaultUserAgentHeader:      true,
		DisableHeaderNamesNormalizing: true,
		DisablePathNormalizing:        true,
		Dial: (&fasthttp.TCPDialer{
			Concurrency:      4096,
			DNSCacheDuration: time.Hour,
		}).Dial,
	}
	return paymentClients{
		primary:  internal.NewPaymentClient(ctx, client, config.primaryURL, config.primaryHealthURL, logger),
		fallback: internal.NewPaymentClient(ctx, client, config.fallbackURL, config.fallbackHealthURL, logger),
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

	paymentHandler := buildPaymentHandler(ctx, config, logger, dbClient)

	return &Server{port: port, handler: paymentHandler, logger: logger}
}

func newLogger() *slog.Logger {
	var levelVar slog.LevelVar
	levelVar.Set(slog.LevelError)

	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: &levelVar,
	}))
}

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
	paymentClients := createPaymentClients(ctx, cfg, logger)
	workerConf := loadWorkerConfiguration(ctx, logger)

	logger.InfoContext(ctx, "Payment handler configuration",
		slog.Int("channel_len", workerConf.channelLen),
		slog.Int("num_workers", workerConf.numWorkers),
	)

	channel := make(chan []byte, workerConf.channelLen)
	router := internal.NewRouter(
		cfg.routerThreshold,
		workerConf.channelLen,
		channel,
		paymentClients.primary,
		paymentClients.fallback,
	)

	return internal.NewPaymentHandler(ctx, router, workerConf.numWorkers, channel, logger, dbClient)
}

func (s *Server) Shutdown(ctx context.Context) {
	s.logger.InfoContext(ctx, "Shutting down server gracefully")
	s.handler.Shutdown()
}

func main() {
	ctx := context.Background()

	server := NewServer()

	server.logger.InfoContext(ctx, "Starting server",
		slog.String("port", server.port),
		slog.String("health_endpoint", "http://localhost:"+server.port+"/health"),
		slog.String("payments_endpoint", "http://localhost:"+server.port+"/payments"),
	)

	go readMetrics()

	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, os.Interrupt, syscall.SIGTERM)

	go func() {
		err := fasthttp.ListenAndServe(":"+server.port, server.HandlerFunc)
		if err != nil {
			panic(err)
		}
	}()

	go func() {
		err := http.ListenAndServe(":6060", nil)
		if err != nil {
			panic(err)
		}
	}()

	<-signalChannel
	server.logger.InfoContext(ctx, "Stopping server")

	server.Shutdown(ctx)
}

func readMetrics() {
	interval := 2 * time.Second

	descs := metrics.All()
	// Prepare stable, sorted order for display.
	names := make([]string, 0, len(descs))
	for _, d := range descs {
		names = append(names, d.Name)
	}
	sort.Strings(names)

	// Prebuild sample slice once (no per-tick allocs).
	samples := make([]metrics.Sample, len(names))
	for i, n := range names {
		samples[i].Name = n
	}

	var lastLines int

	for {
		metrics.Read(samples)

		// Build table lines.
		lines := make([]string, 0, len(samples)+3)
		header := fmt.Sprintf("Go runtime/metrics — %s", time.Now().Format(time.RFC3339))
		sep := repeat('-', 80)
		lines = append(lines, header, sep)
		lines = append(lines, fmt.Sprintf("%-64s | %s", "KEY", "VALUE"))
		lines = append(lines, sep)

		for _, s := range samples {
			val := formatSampleValue(s)
			lines = append(lines, fmt.Sprintf("%-64s | %s", s.Name, val))
		}

		// --- Overwrite previous frame (move cursor up + clear) ---
		// Move the cursor up by the number of lines printed last time.
		if lastLines > 0 {
			fmt.Printf("\x1b[%dA", lastLines) // cursor up N lines
			fmt.Printf("\x1b[J")              // clear from cursor to end of screen
		}

		// Print current frame.
		for _, ln := range lines {
			fmt.Printf("%s\n", ln)
		}
		lastLines = len(lines)

		time.Sleep(interval)
	}
}

func (s *Server) HandlerFunc(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Path()) {
	case "/payments":
		s.paymentsHandler(ctx)
	case "/payments-summary":
		s.paymentsSummaryHandler(ctx)
	default:
		ctx.Error("not found", fasthttp.StatusNotFound)
	}
}

func (s *Server) paymentsHandler(ctx *fasthttp.RequestCtx) {
	if !ctx.IsPost() {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)

		return
	}

	byteBody := []byte(nil)
	byteBody = append(byteBody, ctx.PostBody()...)

	processErr := s.handler.ProcessPayment(ctx, byteBody)
	if processErr != nil {
		ctx.SetStatusCode(http.StatusInternalServerError)

		return
	}

	ctx.SetStatusCode(http.StatusOK)
}

func (s *Server) paymentsSummaryHandler(ctx *fasthttp.RequestCtx) {
	if !ctx.IsGet() {
		ctx.SetStatusCode(http.StatusNotFound)

		return
	}

	fromTime, toTime, err := parseTimeRange(string(ctx.FormValue("from")), string(ctx.FormValue("to")))
	if err != nil {
		ctx.SetStatusCode(http.StatusBadRequest)

		return
	}

	summaries, err := s.handler.GetPaymentsSummary(ctx, fromTime, toTime)
	if err != nil {
		s.logger.ErrorContext(ctx, fmt.Sprintf("Failed to read payments summary, %v", err))
		ctx.SetStatusCode(http.StatusInternalServerError)

		return
	}

	resp := map[string]any{
		"default": map[string]any{
			"totalRequests": summaries[0].TotalRequests,
			"totalAmount":   summaries[0].TotalAmount,
		},
		"fallback": map[string]any{
			"totalRequests": summaries[1].TotalRequests,
			"totalAmount":   summaries[1].TotalAmount,
		},
	}

	bResp, err := json.Marshal(resp)
	if err != nil {
		s.logger.ErrorContext(ctx, fmt.Sprintf("Failed to convert body, %v", err))
		ctx.SetStatusCode(http.StatusInternalServerError)

		return
	}

	ctx.SetStatusCode(http.StatusOK)
	ctx.SetContentType("application-json")
	ctx.Request.Header.SetContentLength(len(bResp))
	ctx.SetBody(bResp)
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

func formatSampleValue(s metrics.Sample) string {
	switch s.Value.Kind() {
	case metrics.KindUint64:
		return fmt.Sprintf("%d", s.Value.Uint64())
	case metrics.KindFloat64:
		return fmt.Sprintf("%.6g", s.Value.Float64())
	case metrics.KindFloat64Histogram:
		h := s.Value.Float64Histogram()
		total := uint64(0)
		for _, c := range h.Counts {
			total += c
		}
		// Compact summary (you can change which quantiles to show)
		p50 := histQuantile(h, 0.50)
		p90 := histQuantile(h, 0.90)
		p99 := histQuantile(h, 0.99)
		return fmt.Sprintf("count=%d p50=%.6g p90=%.6g p99=%.6g", total, p50, p90, p99)
	default:
		return "<unknown>"
	}
}

func histQuantile(h *metrics.Float64Histogram, q float64) float64 {
	if h == nil || len(h.Counts) == 0 {
		return 0
	}
	var total uint64
	for _, c := range h.Counts {
		total += c
	}
	if total == 0 {
		return 0
	}
	target := uint64(float64(total-1) * q)

	var cum uint64
	for i, c := range h.Counts {
		if c == 0 {
			continue
		}
		next := cum + c
		if target < next {
			var lo, hi float64
			if i == 0 {
				// For -Inf bucket, just anchor to first bound distance.
				lo = h.Buckets[0] - 1
			} else {
				lo = h.Buckets[i-1]
			}
			if i == len(h.Buckets) {
				hi = h.Buckets[len(h.Buckets)-1] + 1
			} else {
				hi = h.Buckets[i]
			}
			inBucket := float64(target-cum) / float64(c)
			return lo + (hi-lo)*inBucket
		}
		cum = next
	}
	if len(h.Buckets) > 0 {
		return h.Buckets[len(h.Buckets)-1]
	}
	return 0
}

func repeat(ch rune, n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}
