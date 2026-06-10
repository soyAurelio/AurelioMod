// Command control runs the AurelioMod Control API — the backend for the
// Dashboard, Stripe billing, and workspace management. It exposes a REST API
// on /v1 protected by PASETO v4 auth tokens.
//
// Configuration is via environment variables:
//
//	PORT              — HTTP listen port (default: 8080)
//	NEON_DATABASE_URL — Neon DB connection string (required)
//	PASETO_SECRET_KEY — hex-encoded Ed25519 secret key (required)
package main

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	pasetolib "aidanwoods.dev/go-paseto"
	"github.com/gofiber/fiber/v3"
	_ "github.com/lib/pq"

	controlapi "github.com/soyAurelio/AurelioMod/control/api"
	"github.com/soyAurelio/AurelioMod/control/billing"
	"github.com/soyAurelio/AurelioMod/internal/paseto"
)

// pasetoAdapter wraps internal/paseto.TokenManager to satisfy
// controlapi.TokenManager interface.
type pasetoAdapter struct {
	*paseto.TokenManager
}

func (pa *pasetoAdapter) VerifyToken(signed string) (controlapi.Token, error) {
	tok, err := pa.TokenManager.VerifyToken(signed)
	if err != nil {
		return nil, err
	}
	return &tokenAdapter{tok}, nil
}

// tokenAdapter wraps *pasetolib.Token to satisfy controlapi.Token.
type tokenAdapter struct {
	token *pasetolib.Token
}

func (a *tokenAdapter) Subject() string {
	sub, _ := a.token.GetSubject()
	return sub
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// --- Configuration ---
	port := envOrDefault("PORT", "8080")
	dbURL := os.Getenv("NEON_DATABASE_URL")
	if dbURL == "" {
		logger.Error("NEON_DATABASE_URL is required")
		os.Exit(1)
	}

	pasetoKey := os.Getenv("PASETO_SECRET_KEY")
	if pasetoKey == "" {
		logger.Error("PASETO_SECRET_KEY is required — generate with: go run scripts/genkey.go")
		os.Exit(1)
	}

	// --- Neon DB ---
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		logger.Error("neon db: sql.Open failed", "error", err)
		os.Exit(1)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		logger.Error("neon db: ping failed", "error", err)
		os.Exit(1)
	}
	logger.Info("neon db connected")

	// Run migrations
	if err := migrate(ctx, db); err != nil {
		logger.Error("migration failed", "error", err)
		os.Exit(1)
	}
	logger.Info("migrations applied")

	// --- PASETO Token Manager ---
	tm, err := paseto.NewFromHex(pasetoKey)
	if err != nil {
		logger.Error("paseto: key init failed", "error", err)
		os.Exit(1)
	}
	logger.Info("paseto token manager initialized")

	// --- Fiber App (with PASETO adapter) ---
	app := controlapi.New(db, &pasetoAdapter{tm})

	// --- Stripe Billing (optional, gated by STRIPE_SECRET_KEY) ---
	if stripeKey := os.Getenv("STRIPE_SECRET_KEY"); stripeKey != "" {
		bh := billing.New(db, stripeKey,
			os.Getenv("STRIPE_WEBHOOK_SECRET"),
			os.Getenv("CONTROL_BASE_URL"),
		)

		// Billing routes (auth required)
		authMW := controlapi.AuthMiddleware(&pasetoAdapter{tm})
		app.Post("/v1/billing/checkout", authMW, bh.HandleCheckout)
		app.Post("/v1/billing/portal", authMW, bh.HandlePortal)

		// Stripe webhook (no auth — Stripe signs requests)
		app.Post("/v1/webhooks/stripe", bh.HandleWebhook)

		// Redirect pages (after checkout)
		app.Get("/billing/success", func(c fiber.Ctx) error {
			return c.Status(200).JSON(fiber.Map{"status": "ok", "message": "payment successful", "session_id": c.Query("session_id")})
		})
		app.Get("/billing/cancel", func(c fiber.Ctx) error {
			return c.Status(200).JSON(fiber.Map{"status": "cancelled", "message": "payment cancelled"})
		})

		logger.Info("stripe billing enabled")
	} else {
		logger.Warn("STRIPE_SECRET_KEY not set — billing disabled")
	}

	// --- Signals ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("control api starting", "port", port)
		if err := app.Listen(":" + port); err != nil {
			logger.Error("server error", "error", err)
		}
	}()

	<-sigCh
	logger.Info("shutting down")

	if err := app.Shutdown(); err != nil {
		logger.Error("shutdown error", "error", err)
	}

	if err := db.Close(); err != nil {
		logger.Error("db close error", "error", err)
	}

	logger.Info("control api stopped")
}

// envOrDefault returns the env var value or fallback if unset.
func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// migrate applies pending database migrations.
func migrate(ctx context.Context, db *sql.DB) error {
	migrations := []string{
		// 001: audit_log — already applied by engine service, idempotent
		`CREATE TABLE IF NOT EXISTS audit_log (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			audit_id TEXT NOT NULL UNIQUE,
			workspace_id TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			decision TEXT NOT NULL,
			confidence REAL,
			category TEXT,
			analyst_version TEXT,
			normalization_pipeline TEXT,
			processing_ms INTEGER,
			timestamp_utc TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		// 002: workspaces
		`CREATE TABLE IF NOT EXISTS workspaces (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name TEXT NOT NULL,
			api_key TEXT NOT NULL UNIQUE,
			plan TEXT NOT NULL DEFAULT 'bronze',
			stripe_customer_id TEXT,
			stripe_subscription_id TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
	}

	for i, m := range migrations {
		if _, err := db.ExecContext(ctx, m); err != nil {
			return err
		}
		slog.Info("migration applied", "index", i)
	}

	// Ensure indexes exist (idempotent)
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_audit_workspace_time ON audit_log(workspace_id, timestamp_utc)`,
	}
	for _, idx := range indexes {
		if _, err := db.ExecContext(ctx, idx); err != nil {
			return err
		}
	}

	// Alter table migrations (idempotent — ignore errors if column exists)
	alters := []string{
		`ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS stripe_customer_id TEXT`,
		`ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS stripe_subscription_id TEXT`,
		`ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS monthly_analysis_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS monthly_analysis_limit INTEGER NOT NULL DEFAULT 1000`,
	}
	for _, a := range alters {
		if _, err := db.ExecContext(ctx, a); err != nil {
			slog.Warn("alter table migration failed (may already exist)", "error", err)
		}
	}

	return nil
}
