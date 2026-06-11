package controlapi

import (
	"database/sql"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
)

// New creates a Fiber v3 app with all Control API routes and middleware.
//
// Routes:
//
//	GET    /healthz                  — health check (no auth)
//	POST   /v1/auth/login            — workspace login
//	POST   /v1/auth/refresh          — refresh token
//	GET    /v1/workspaces            — list workspaces
//	POST   /v1/workspaces            — create workspace
//	GET    /v1/workspaces/:id         — get workspace
//	GET    /v1/workspaces/:id/stats   — workspace stats
//	GET    /v1/workspaces/:id/decisions           — decision history
//	GET    /v1/workspaces/:id/decisions/:audit_id  — single decision
func New(db *sql.DB, tm TokenManager) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "AurelioMod Control API",
		ServerHeader: "AurelioMod",
		Immutable:    false,
	})

	// Global middleware
	app.Use(recover.New())

	// Health check — no auth needed
	app.Get("/healthz", func(c fiber.Ctx) error {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "ok"})
	})

	// Handlers
	auth := NewAuthHandler(db, tm)
	workspaces := NewWorkspaceHandler(db)
	decisions := NewDecisionsHandler(db)

	// --- v1 API ---
	v1 := app.Group("/v1")

	// Auth (public, no middleware for login and verify)
	v1.Post("/auth/login", auth.HandleLogin)
	v1.Get("/auth/verify", AuthMiddleware(tm), auth.HandleVerify)
	v1.Post("/auth/refresh", AuthMiddleware(tm), auth.HandleRefresh)

	// Workspaces (auth required)
	v1.Get("/workspaces", AuthMiddleware(tm), workspaces.HandleList)
	v1.Post("/workspaces", AuthMiddleware(tm), workspaces.HandleCreate)
	v1.Get("/workspaces/:id", AuthMiddleware(tm), workspaces.HandleGet)
	v1.Get("/workspaces/:id/stats", AuthMiddleware(tm), workspaces.HandleStats)
	v1.Post("/workspaces/:id/consume", AuthMiddleware(tm), workspaces.HandleConsume)

	// Decisions (auth required)
	v1.Get("/workspaces/:id/decisions", AuthMiddleware(tm), decisions.HandleListDecisions)
	v1.Get("/workspaces/:id/decisions/:audit_id", AuthMiddleware(tm), decisions.HandleGetDecision)

	// MFA (auth required)
	mfaSvc := NewMFA(Config{Issuer: "AurelioMod", DB: db})
	mfaHandler := NewMFAHandler(mfaSvc)
	v1.Post("/workspaces/:id/mfa/enroll", AuthMiddleware(tm), mfaHandler.HandleBeginEnrollment)
	v1.Post("/workspaces/:id/mfa/confirm", AuthMiddleware(tm), mfaHandler.HandleConfirmEnrollment)
	v1.Get("/workspaces/:id/mfa/status", AuthMiddleware(tm), mfaHandler.HandleStatus)
	v1.Delete("/workspaces/:id/mfa", AuthMiddleware(tm), mfaHandler.HandleDisable)

	return app
}

// AuthMiddleware validates PASETO v4 Bearer tokens.
// On success, it stores the workspace_id in c.Locals.
func AuthMiddleware(tm TokenManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "missing or malformed Authorization header. Use: Bearer <token>",
			})
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		token, err := tm.VerifyToken(tokenStr)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid or expired token",
			})
		}

		c.Locals("workspace_id", token.Subject())
		return c.Next()
	}
}
