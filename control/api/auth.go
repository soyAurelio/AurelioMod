package controlapi

import (
	"database/sql"
	"time"

	"github.com/gofiber/fiber/v3"
)

// AuthHandler handles workspace authentication via PASETO v4 tokens.
// Login: workspace provides api_key → valid for 24h.
// Refresh: valid token → new 24h token.
type AuthHandler struct {
	db       *sql.DB
	tokenMgr TokenManager
	tokenTTL time.Duration
}

// TokenManager abstracts PASETO token operations for auth.
// internal/paseto.TokenManager satisfies this interface.
type TokenManager interface {
	ServiceToken(serviceName string, ttl time.Duration) (string, error)
	VerifyToken(signed string) (Token, error)
}

// Token abstracts a verified PASETO token's claims.
type Token interface {
	Subject() string
}

// NewAuthHandler creates an AuthHandler with the given database and token manager.
func NewAuthHandler(db *sql.DB, tm TokenManager) *AuthHandler {
	return &AuthHandler{
		db:       db,
		tokenMgr: tm,
		tokenTTL: 24 * time.Hour,
	}
}

// loginRequest is the JSON body for POST /v1/auth/login.
type loginRequest struct {
	APIKey string `json:"api_key"`
}

// loginResponse is the JSON body returned on successful login.
type loginResponse struct {
	Token     string `json:"token"`
	ExpiresIn int64  `json:"expires_in"` // seconds
}

// HandleLogin validates a workspace api_key and issues a PASETO token.
//
//	POST /v1/auth/login
//	Body: {"api_key": "ws_..."}
//	200:  {"token": "v4.local...", "expires_in": 86400}
//	401:  {"error": "invalid api_key"}
func (h *AuthHandler) HandleLogin(c fiber.Ctx) error {
	var req loginRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.APIKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "api_key is required",
		})
	}

	var workspaceID string
	err := h.db.QueryRowContext(
		c.Context(),
		"SELECT id FROM workspaces WHERE api_key = $1",
		req.APIKey,
	).Scan(&workspaceID)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "invalid api_key",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "auth lookup failed",
		})
	}

	token, err := h.tokenMgr.ServiceToken(workspaceID, h.tokenTTL)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "token generation failed",
		})
	}

	return c.Status(fiber.StatusOK).JSON(loginResponse{
		Token:     token,
		ExpiresIn: int64(h.tokenTTL.Seconds()),
	})
}

// HandleRefresh validates an existing PASETO token and issues a new one.
//
//	POST /v1/auth/refresh
//	Header: Authorization: Bearer <token>
//	200:  {"token": "v4.local...", "expires_in": 86400}
//	401:  {"error": "invalid or expired token"}
func (h *AuthHandler) HandleRefresh(c fiber.Ctx) error {
	workspaceID, ok := c.Locals("workspace_id").(string)
	if !ok || workspaceID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "missing workspace identity",
		})
	}

	token, err := h.tokenMgr.ServiceToken(workspaceID, h.tokenTTL)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "token refresh failed",
		})
	}

	return c.Status(fiber.StatusOK).JSON(loginResponse{
		Token:     token,
		ExpiresIn: int64(h.tokenTTL.Seconds()),
	})
}
