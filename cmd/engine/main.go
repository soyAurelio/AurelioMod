// Command engine runs the AurelioMod content analysis Engine service.
// It wires together the full pipeline (L1→L2→L3 cache hierarchy, WaveSpeed
// AI analysis, audit logging, NATS decision publishing, and quarantine
// management) behind a ConnectRPC HTTP server.
//
// Configuration is via environment variables:
//
//	PORT                     — HTTP listen port (default: 8080)
//	WAVESPEED_API_KEY        — WaveSpeed API key (required for analysis)
//	WAVESPEED_API_URL        — WaveSpeed API base URL (default: https://api.wavespeed.ai)
//	DRAGONFLY_ADDR           — DragonflyDB address (default: localhost:6380)
//	NATS_URL                 — NATS server URL (default: nats://localhost:4222)
//	WEAVIATE_ADDR            — Weaviate HTTP base URL (default: http://localhost:8090)
//	OTEL_EXPORTER_OTLP_ENDPOINT — OTLP collector endpoint (default: none = noop)
//	OTEL_SERVICE_NAME        — OpenTelemetry service name (default: engine)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/soyAurelio/AurelioMod/engine/analyzer"
	"github.com/soyAurelio/AurelioMod/engine/audit"
	"github.com/soyAurelio/AurelioMod/engine/hasher"
	"github.com/soyAurelio/AurelioMod/engine/media"
	engineNats "github.com/soyAurelio/AurelioMod/engine/nats"
	"github.com/soyAurelio/AurelioMod/engine/pipeline"
	"github.com/soyAurelio/AurelioMod/engine/safety"
	"github.com/soyAurelio/AurelioMod/engine/service"
	"github.com/soyAurelio/AurelioMod/engine/telemetry"
	"github.com/soyAurelio/AurelioMod/internal/auth"
	"github.com/soyAurelio/AurelioMod/internal/cache"
	internalnats "github.com/soyAurelio/AurelioMod/internal/nats"
	"github.com/soyAurelio/AurelioMod/internal/paseto"
	"github.com/soyAurelio/AurelioMod/internal/weaviate"
	"github.com/soyAurelio/AurelioMod/proto/aureliomod/v1/aureliomodv1connect"
)

// serverConfig holds all configuration for the Engine server,
// loaded from environment variables.
type serverConfig struct {
	Port          string
	WaveSpeedKey  string
	WaveSpeedURL  string
	DragonflyAddr string
	NATSURL       string
	WeaviateAddr  string
	OTLPEndpoint  string
	ServiceName   string
}

// loadConfig reads configuration from environment variables with sensible defaults.
func loadConfig() serverConfig {
	return serverConfig{
		Port:          envOrDefault("PORT", "8080"),
		WaveSpeedKey:  os.Getenv("WAVESPEED_API_KEY"),
		WaveSpeedURL:  envOrDefault("WAVESPEED_API_URL", "https://api.wavespeed.ai"),
		DragonflyAddr: envOrDefault("DRAGONFLY_ADDR", "localhost:6380"),
		NATSURL:       envOrDefault("NATS_URL", "nats://localhost:4222"),
		WeaviateAddr:  envOrDefault("WEAVIATE_ADDR", "http://localhost:8090"),
		OTLPEndpoint:  os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		ServiceName:   envOrDefault("OTEL_SERVICE_NAME", "engine"),
	}
}

// envOrDefault returns the environment variable value, or fallback if unset.
func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// setGOMAXPROCS reserves one logical CPU for FFmpeg subprocesses.
// It returns the value passed to runtime.GOMAXPROCS.
// Exported for testing.
func setGOMAXPROCS(numCPU int) int {
	n := max(1, numCPU-1)
	runtime.GOMAXPROCS(n)
	return n
}

// init configures runtime settings before main() executes.
func init() {
	setGOMAXPROCS(runtime.NumCPU())
}

func main() {
	// Structured JSON logger (production default)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg := loadConfig()

	// Start pprof admin server on separate port with PASETO auth
	if key := os.Getenv("PPROF_ADMIN_KEY"); key != "" {
		pprofTm, err := newPprofTokenManager(key)
		if err != nil {
			slog.Error("pprof token manager init failed", "error", err)
			os.Exit(1)
		}
		go startPprofServer(pprofTm)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Initialize OpenTelemetry
	tele, err := telemetry.Init(ctx, telemetry.Config{
		OTLPEndpoint: cfg.OTLPEndpoint,
		ServiceName:  cfg.ServiceName,
	})
	if err != nil {
		slog.ErrorContext(ctx, "telemetry init failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tele.Shutdown(shutdownCtx); err != nil {
			slog.Error("telemetry shutdown failed", "error", err)
		}
	}()

	srv, err := newServer(ctx, cfg)
	if err != nil {
		slog.ErrorContext(ctx, "server setup failed", "error", err)
		os.Exit(1)
	}

	// Start server in background
	errCh := make(chan error, 1)
	go func() {
		slog.Info("engine server starting", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for shutdown signal or server error
	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	slog.Info("shutting down engine server")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown failed", "error", err)
		os.Exit(1)
	}

	slog.Info("engine server stopped")
}

// newServer wires all dependencies and returns a configured HTTP server.
// Dependencies that require external services (WaveSpeed, NATS, DragonflyDB,
// Weaviate) are optional — when their config is empty, they are skipped
// and the server operates in degraded mode (returning QUEUED decisions).
func newServer(ctx context.Context, cfg serverConfig) (*http.Server, error) {
	mux := http.NewServeMux()

	// Health check endpoint (always available)
	mux.HandleFunc("GET /healthz", healthHandler)

	// --- Wire dependencies ---

	// L1/L2 Cache — DragonflyDB (required)
	cacheClient := cache.NewCacheClient(cache.CacheClientConfig{
		Addr:     cfg.DragonflyAddr,
		PoolSize: 200,
	})
	slog.InfoContext(ctx, "cache client created", "addr", cfg.DragonflyAddr)

	// Content normalizer with nsjail sandbox gate
	sandboxEnabled := os.Getenv("MEDIA_SANDBOX_ENABLED") != "false" // default: true
	ffmpegRunner := media.NewNsJailFFmpeg("/usr/bin/nsjail", "/usr/bin/ffmpeg", sandboxEnabled)
	normalizer := hasher.NewNormalizer(ffmpegRunner)
	slog.InfoContext(ctx, "content normalizer created",
		"sandbox", sandboxEnabled,
	)

	// Safe Browsing URL reputation service
	sbEnabled := os.Getenv("SAFEBROWSING_ENABLED") != "false" // default: true
	sbService := safety.NewSafeBrowsingService(safety.SafeBrowsingConfig{
		RDB:    cacheClient.RDB(), // Access the underlying go-redis client for SETEX caching
		Enabled: sbEnabled,
	})
	if sbEnabled {
		slog.InfoContext(ctx, "Safe Browsing service created", "cache_ttl", "15m")
	} else {
		slog.WarnContext(ctx, "SAFEBROWSING_ENABLED=false — URL safety checks disabled")
	}
	_ = sbService // wired for future URL-sourced content pipeline integration

	// WaveSpeed analyzer (optional)
	var waveSpeed analyzer.Analyzer
	if cfg.WaveSpeedKey != "" {
		waveSpeed = analyzer.NewWaveSpeedClient(cfg.WaveSpeedURL, cfg.WaveSpeedKey)
		slog.InfoContext(ctx, "wavespeed client created", "url", cfg.WaveSpeedURL)
	} else {
		slog.WarnContext(ctx, "WAVESPEED_API_KEY not set — analysis disabled, returning QUEUED")
	}

	// Weaviate L3 client (optional)
	var wvClient weaviate.WeaviateClient
	if cfg.WeaviateAddr != "" {
		wvClient = weaviate.NewHTTPClient(cfg.WeaviateAddr)
		slog.InfoContext(ctx, "weaviate client created", "addr", cfg.WeaviateAddr)
	}

	// --- Integration hooks ---

	// Audit emitter (slog JSON → stdout only; Neon/R2 are nil until provisioned)
	auditLogger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	auditEmitter := audit.NewMultiEmitter(auditLogger, nil, nil)

	// NATS decision publisher (optional)
	var decisionHook pipeline.DecisionHook
	if cfg.NATSURL != "" {
		natClient, err := internalnats.Connect(internalnats.Config{
			URL: cfg.NATSURL,
		})
		if err != nil {
			return nil, fmt.Errorf("nats connect: %w", err)
		}
		natPublisher := engineNats.NewNATSPublisher(natClient.Conn())
		slog.InfoContext(ctx, "nats connected", "url", cfg.NATSURL)

		decisionHook = func(ctx context.Context, workspaceID, contentHash, decision, category string, confidence float64) {
			evt := &engineNats.DecisionEvent{
				DecisionID:  fmt.Sprintf("dec_%s_%d", contentHash[:12], time.Now().UnixNano()),
				WorkspaceID: workspaceID,
				ContentHash: contentHash,
				Decision:    decision,
				Category:    category,
				Confidence:  confidence,
				Timestamp:   time.Now(),
			}
			_ = natPublisher.PublishDecision(ctx, evt)
		}
	} else {
		slog.WarnContext(ctx, "NATS_URL not set — decision publishing disabled")
	}

	// Audit hook (fire-and-forget audit emission)
	auditHook := func(ctx context.Context, workspaceID, contentHash, decision, category string, confidence float64, processingMs int64) {
		evt := audit.AuditEvent{
			AuditID:               fmt.Sprintf("evt_%s_%d", contentHash[:12], time.Now().UnixNano()),
			WorkspaceID:           workspaceID,
			ContentHash:           contentHash,
			Decision:              decision,
			Confidence:            confidence,
			Category:              category,
			AnalystVersion:        "wavespeed-v3.2",
			NormalizationPipeline: "480p+strip_exif+jpeg_q85",
			ProcessingMs:          processingMs,
			TimestampUTC:          time.Now(),
		}
		_ = auditEmitter.Emit(ctx, evt)
	}

	// --- Build pipeline ---
	pipe := pipeline.New(
		cacheClient,   // L1 + L2 cache
		cacheClient,   // L2 cache (same DragonflyDB client)
		normalizer,
		waveSpeed,
		wvClient,
		pipeline.WithAuditHook(auditHook),
		pipeline.WithDecisionHook(decisionHook),
	)

	slog.InfoContext(ctx, "pipeline initialized")

	// --- PASETO auth interceptor (gated by PASETO_AUTH_ENABLED) ---
	var handlerOpts []connect.HandlerOption
	if os.Getenv("PASETO_AUTH_ENABLED") == "true" {
		keyHex := os.Getenv("PASETO_SECRET_KEY")
		if keyHex == "" {
			return nil, fmt.Errorf("PASETO_AUTH_ENABLED=true requires PASETO_SECRET_KEY")
		}
		tm, err := paseto.NewFromHex(keyHex)
		if err != nil {
			return nil, fmt.Errorf("paseto auth init: %w", err)
		}
		authInterceptor := auth.NewPASETOInterceptor(tm.PublicKey())
		handlerOpts = append(handlerOpts, connect.WithInterceptors(authInterceptor))
		slog.InfoContext(ctx, "paseto auth interceptor enabled")
	}

	// --- ConnectRPC handler ---
	enforceMIME := os.Getenv("ENFORCE_MIME") == "true"
	handler := service.NewHandler(pipe, enforceMIME)
	path, h := aureliomodv1connect.NewContentAnalysisServiceHandler(handler, handlerOpts...)
	mux.Handle(path, h)

	slog.InfoContext(ctx, "connectrpc handler registered", "path", path)

	return &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}, nil
}

// healthHandler responds with a 200 OK JSON body for health checks.
// Used by Docker HEALTHCHECK and load balancers.
func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}


