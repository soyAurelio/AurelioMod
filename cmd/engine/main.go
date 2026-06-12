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
//	WEBRISK_API_KEY          — Google Web Risk API key (optional, ADC fallback)
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
	"github.com/soyAurelio/AurelioMod/engine/quarantine"
	"github.com/soyAurelio/AurelioMod/engine/safety"
	"github.com/soyAurelio/AurelioMod/engine/service"
	"github.com/soyAurelio/AurelioMod/engine/telemetry"
	"github.com/soyAurelio/AurelioMod/internal/auth"
	"github.com/soyAurelio/AurelioMod/internal/cache"
	"github.com/soyAurelio/AurelioMod/internal/env"
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
		Port:          env.Get("PORT", "8080"),
		WaveSpeedKey:  os.Getenv("WAVESPEED_API_KEY"),
		WaveSpeedURL:  env.Get("WAVESPEED_API_URL", "https://api.wavespeed.ai"),
		DragonflyAddr: env.Get("DRAGONFLY_ADDR", "localhost:6380"),
		NATSURL:       env.Get("NATS_URL", "nats://localhost:4222"),
		WeaviateAddr:  env.Get("WEAVIATE_ADDR", "http://localhost:8090"),
		OTLPEndpoint:  os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		ServiceName:   env.Get("OTEL_SERVICE_NAME", "engine"),
	}
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
	// Health check mode: Docker HEALTHCHECK in Distroless images.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		port := env.Get("PORT", "8080")
		resp, err := http.Get("http://localhost:" + port + "/healthz")
		if err != nil {
			os.Exit(1)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

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
	// Health check (basic — load balancer)
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

	// Deep health check: verifies FFmpeg+nsjail+glibc runtime stack.
	// This is the "blast radius center" smoke test.
	mux.HandleFunc("GET /healthz/deep", deepHealthHandler(ffmpegRunner))

	// Web Risk URL reputation service.
	// By default, WebRisk is best-effort: if credentials are unavailable, the
	// service starts disabled and logs a warning. Set WEBRISK_REQUIRED=true in
	// production to force a fatal error when URL safety cannot be initialized.
	wrEnabled := os.Getenv("WEBRISK_ENABLED") != "false"  // default: true
	wrRequired := os.Getenv("WEBRISK_REQUIRED") == "true" // default: false
	wrService, err := safety.NewWebRiskService(ctx, safety.WebRiskConfig{
		RDB:     cacheClient.RDB(),
		Enabled: wrEnabled,
	})
	if err != nil {
		if wrRequired {
			return nil, fmt.Errorf("WEBRISK_REQUIRED=true but webrisk init failed: %w", err)
		}
		slog.WarnContext(ctx, "webrisk init failed — URL safety checks disabled",
			"error", err,
		)
		// Create a disabled service so the server can still start
		wrService, _ = safety.NewWebRiskService(ctx, safety.WebRiskConfig{
			RDB:     nil,
			Enabled: false,
		})
	} else if wrEnabled {
		slog.InfoContext(ctx, "Web Risk service created", "cache_ttl", "15m")
	} else {
		slog.WarnContext(ctx, "WEBRISK_ENABLED=false — URL safety checks disabled")
	}
	slog.InfoContext(ctx, "webrisk wired into pipeline URL safety gate")

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

	// Audit emitter chain: slog (critical) + Neon DB (best-effort, gated)
	// + R2 cold storage (best-effort, gated).
	auditLogger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Neon DB audit emitter — gated by NEON_AUDIT_ENABLED env var.
	// When disabled (default), neonStore is nil and MultiEmitter skips it.
	var neonStore audit.NeonStore
	if os.Getenv("NEON_AUDIT_ENABLED") == "true" {
		url := os.Getenv("NEON_DATABASE_URL")
		if url == "" {
			slog.WarnContext(ctx, "NEON_AUDIT_ENABLED=true but NEON_DATABASE_URL not set — skipping Neon emitter")
		} else {
			ne, err := audit.NewNeonEmitter(ctx, url)
			if err != nil {
				slog.WarnContext(ctx, "neon audit emitter init failed", "error", err)
			} else {
				neonStore = ne
				slog.InfoContext(ctx, "neon audit emitter enabled")
			}
		}
	}

	// R2 cold storage audit emitter — gated by R2_AUDIT_ENABLED env var.
	// Uses MinIO in dev, Cloudflare R2 in production (both S3-compatible).
	var r2Store audit.R2Store
	if os.Getenv("R2_AUDIT_ENABLED") == "true" {
		s3store, err := audit.NewS3AuditStoreFromEnv(ctx)
		if err != nil {
			slog.WarnContext(ctx, "r2 audit store init failed", "error", err)
		} else if s3store != nil {
			r2Store = s3store
			slog.InfoContext(ctx, "r2 audit store enabled",
				slog.String("endpoint", os.Getenv("R2_ENDPOINT")),
				slog.String("bucket", os.Getenv("R2_BUCKET")),
			)
		}
	}

	auditEmitter := audit.NewMultiEmitter(auditLogger, neonStore, r2Store)

	// NATS decision publisher (best-effort — Engine starts without NATS)
	var decisionHook pipeline.DecisionHook
	if cfg.NATSURL != "" {
		natClient, err := internalnats.Connect(internalnats.Config{
			URL: cfg.NATSURL,
		})
		if err != nil {
			slog.WarnContext(ctx, "nats connect failed — decision publishing disabled",
				"error", err,
			)
		} else {
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
	// Quarantine inversion: block-first, analyze-later state machine.
	// Uses the same DragonflyDB connection as L1/L2 cache.
	// Gated by QUARANTINE_ENABLED env var (default: false for dev).
	var quarantineHook pipeline.QuarantineHook
	if os.Getenv("QUARANTINE_ENABLED") == "true" {
		quarantineStore := quarantine.NewDragonflyStore(cacheClient.RDB(), "quarantine:")
		qm := quarantine.NewQuarantineManager(quarantineStore, quarantine.DefaultTTL)
		quarantineHook = func(ctx context.Context, contentID, decision, category string, confidence float64) {
			if _, err := qm.UpdateStatus(ctx, contentID, decision, category, confidence); err != nil {
				slog.WarnContext(ctx, "quarantine update failed",
					"content_id", contentID,
					"error", err,
				)
			}
		}
		slog.InfoContext(ctx, "quarantine hook enabled (cuarentena invertida)")
	}

	pipe := pipeline.New(
		cacheClient, // L1 + L2 cache
		cacheClient, // L2 cache (same DragonflyDB client)
		normalizer,
		waveSpeed,
		wvClient,
		pipeline.WithURLChecker(wrService),
		pipeline.WithAuditHook(auditHook),
		pipeline.WithDecisionHook(decisionHook),
		pipeline.WithQuarantineHook(quarantineHook),
	)

	slog.InfoContext(ctx, "pipeline initialized")

	// --- PASETO auth interceptor (gated by PASETO_AUTH_ENABLED) ---
	var handlerOpts []connect.HandlerOption
	if os.Getenv("PASETO_AUTH_ENABLED") == "true" {
		keyHex := os.Getenv("PASETO_SECRET_KEY")
		if keyHex == "" {
			return nil, fmt.Errorf("PASETO_AUTH_ENABLED=true requires PASETO_SECRET_KEY")
		}
		prevKeyHex := os.Getenv("PASETO_PREVIOUS_KEY")
		tm, err := paseto.NewRotatable(keyHex, prevKeyHex)
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

	// Wrap mux with per-request body size limit (50 MB).
	// Prevents OOM from oversized Analyze requests.
	const maxBodySize = 50 << 20 // 50 MiB
	limitedHandler := http.MaxBytesHandler(mux, maxBodySize)

	return &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           limitedHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
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

// deepHealthHandler verifies the full runtime stack: FFmpeg + nsjail.
// This is the "smoke test" health check — it proves the Engine's blast
// radius center (FFmpeg + nsjail + glibc + syscalls) actually works.
//
// GET /healthz/deep
// 200: {"status":"ok", "ffmpeg":"7.1", "nsjail":true}
// 503: {"status":"degraded", "error":"..."}
func deepHealthHandler(ffmpegRunner media.FFmpegRunner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Smoke test: decode a 1x1 black JPEG pixel via FFmpeg + nsjail.
		// This proves: glibc loaded, libz found, FFmpeg binary works,
		// nsjail sandbox active, pipe I/O functional.
		// Minimal JPEG: 1x1 black pixel, 631 bytes.
		minimalJPEG := []byte{
			0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46,
			0x49, 0x46, 0x00, 0x01, 0x01, 0x00, 0x00, 0x01,
			0x00, 0x01, 0x00, 0x00, 0xFF, 0xDB, 0x00, 0x43,
			0x00, 0x08, 0x06, 0x06, 0x07, 0x06, 0x05, 0x08,
			0x07, 0x07, 0x07, 0x09, 0x09, 0x08, 0x0A, 0x0C,
			0x14, 0x0D, 0x0C, 0x0B, 0x0B, 0x0C, 0x19, 0x12,
			0x13, 0x0F, 0x14, 0x1D, 0x1A, 0x1F, 0x1E, 0x1D,
			0x1A, 0x1C, 0x1C, 0x20, 0x24, 0x2E, 0x27, 0x20,
			0x22, 0x2C, 0x23, 0x1C, 0x1C, 0x28, 0x37, 0x29,
			0x2C, 0x30, 0x31, 0x34, 0x34, 0x34, 0x1F, 0x27,
			0x39, 0x3D, 0x38, 0x32, 0x3C, 0x2E, 0x33, 0x34,
			0x32, 0xFF, 0xC0, 0x00, 0x0B, 0x08, 0x00, 0x01,
			0x00, 0x01, 0x01, 0x01, 0x11, 0x00, 0xFF, 0xC4,
			0x00, 0x1F, 0x00, 0x00, 0x01, 0x05, 0x01, 0x01,
			0x01, 0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04,
			0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0xFF,
			0xC4, 0x00, 0xB5, 0x10, 0x00, 0x02, 0x01, 0x03,
			0x03, 0x02, 0x04, 0x03, 0x05, 0x05, 0x04, 0x04,
			0x00, 0x00, 0x01, 0x7D, 0x01, 0x02, 0x03, 0x00,
			0x04, 0x11, 0x05, 0x12, 0x21, 0x31, 0x41, 0x06,
			0x13, 0x51, 0x61, 0x07, 0x22, 0x71, 0x14, 0x32,
			0x81, 0x91, 0xA1, 0x08, 0x23, 0x42, 0xB1, 0xC1,
			0x15, 0x52, 0xD1, 0xF0, 0x24, 0x33, 0x62, 0x72,
			0x82, 0x09, 0x0A, 0x16, 0x17, 0x18, 0x19, 0x1A,
			0x25, 0x26, 0x27, 0x28, 0x29, 0x2A, 0x34, 0x35,
			0x36, 0x37, 0x38, 0x39, 0x3A, 0x43, 0x44, 0x45,
			0x46, 0x47, 0x48, 0x49, 0x4A, 0x53, 0x54, 0x55,
			0x56, 0x57, 0x58, 0x59, 0x5A, 0x63, 0x64, 0x65,
			0x66, 0x67, 0x68, 0x69, 0x6A, 0x73, 0x74, 0x75,
			0x76, 0x77, 0x78, 0x79, 0x7A, 0x83, 0x84, 0x85,
			0x86, 0x87, 0x88, 0x89, 0x8A, 0x92, 0x93, 0x94,
			0x95, 0x96, 0x97, 0x98, 0x99, 0x9A, 0xA2, 0xA3,
			0xA4, 0xA5, 0xA6, 0xA7, 0xA8, 0xA9, 0xAA, 0xB2,
			0xB3, 0xB4, 0xB5, 0xB6, 0xB7, 0xB8, 0xB9, 0xBA,
			0xC2, 0xC3, 0xC4, 0xC5, 0xC6, 0xC7, 0xC8, 0xC9,
			0xCA, 0xD2, 0xD3, 0xD4, 0xD5, 0xD6, 0xD7, 0xD8,
			0xD9, 0xDA, 0xE1, 0xE2, 0xE3, 0xE4, 0xE5, 0xE6,
			0xE7, 0xE8, 0xE9, 0xEA, 0xF1, 0xF2, 0xF3, 0xF4,
			0xF5, 0xF6, 0xF7, 0xF8, 0xF9, 0xFA, 0xFF, 0xDA,
			0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3F, 0x00,
			0xD2, 0xCF, 0x20, 0xFF, 0xD9,
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		normalizer := hasher.NewNormalizer(ffmpegRunner)
		result, err := normalizer.Normalize(ctx, minimalJPEG)
		if err != nil {
			slog.Error("deep health check: ffmpeg+nsjail failed",
				"error", err,
			)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"degraded","ffmpeg":"unreachable","nsjail":false}`))
			return
		}

		// Verify we got valid output (1 pixel = 3 bytes RGB)
		if len(result.RGBPixels) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"degraded","ffmpeg":"empty_output","nsjail":true}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"status":"ok","ffmpeg":"%s","nsjail":%v}`,
			hasher.FFmpegVersion,
			os.Getenv("MEDIA_SANDBOX_ENABLED") != "false",
		)))
	}
}
