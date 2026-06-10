package controlapi

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"

	"github.com/gofiber/fiber/v3"
)

// WorkspaceHandler handles CRUD operations for workspaces.
type WorkspaceHandler struct {
	db *sql.DB
}

// NewWorkspaceHandler creates a WorkspaceHandler.
func NewWorkspaceHandler(db *sql.DB) *WorkspaceHandler {
	return &WorkspaceHandler{db: db}
}

// --- Request / Response types ---

type createWorkspaceRequest struct {
	Name string `json:"name"`
}

type workspaceResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	APIKey    string `json:"api_key,omitempty"` // only on creation
	Plan      string `json:"plan"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// --- Handlers ---

// HandleCreate creates a new workspace and returns its api_key.
//
//	POST /v1/workspaces
//	Body: {"name": "My Discord Server"}
//	201:  {"id": "...", "name": "...", "api_key": "ws_...", "plan": "bronze", ...}
func (h *WorkspaceHandler) HandleCreate(c fiber.Ctx) error {
	var req createWorkspaceRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "name is required",
		})
	}

	apiKey := "ws_" + generateAPIKey(32)

	var ws workspaceResponse
	err := h.db.QueryRowContext(c.Context(), `
		INSERT INTO workspaces (name, api_key)
		VALUES ($1, $2)
		RETURNING id, name, plan, created_at, updated_at
	`, req.Name, apiKey).Scan(&ws.ID, &ws.Name, &ws.Plan, &ws.CreatedAt, &ws.UpdatedAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create workspace",
		})
	}
	ws.APIKey = apiKey

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"data": ws,
	})
}

// HandleList returns all workspaces.
//
//	GET /v1/workspaces
//	200: {"data": [...]}
func (h *WorkspaceHandler) HandleList(c fiber.Ctx) error {
	rows, err := h.db.QueryContext(c.Context(), `
		SELECT id, name, plan, created_at, updated_at
		FROM workspaces
		ORDER BY created_at DESC
	`)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to query workspaces",
		})
	}
	defer rows.Close()

	var workspaces []workspaceResponse
	for rows.Next() {
		var ws workspaceResponse
		if err := rows.Scan(&ws.ID, &ws.Name, &ws.Plan, &ws.CreatedAt, &ws.UpdatedAt); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan workspace",
			})
		}
		workspaces = append(workspaces, ws)
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"data": workspaces,
	})
}

// HandleGet returns a single workspace by ID.
//
//	GET /v1/workspaces/:id
//	200: {"data": {...}}
//	404: {"error": "workspace not found"}
func (h *WorkspaceHandler) HandleGet(c fiber.Ctx) error {
	id := c.Params("id")

	var ws workspaceResponse
	err := h.db.QueryRowContext(c.Context(), `
		SELECT id, name, plan, created_at, updated_at
		FROM workspaces WHERE id = $1
	`, id).Scan(&ws.ID, &ws.Name, &ws.Plan, &ws.CreatedAt, &ws.UpdatedAt)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "workspace not found",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch workspace",
		})
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"data": ws,
	})
}

// HandleStats returns aggregated stats for a workspace.
//
//	GET /v1/workspaces/:id/stats
//	200: {"data": {"total_decisions": 1234, "block_rate": 0.15, ...}}
func (h *WorkspaceHandler) HandleStats(c fiber.Ctx) error {
	id := c.Params("id")

	// Verify workspace exists
	var exists bool
	if err := h.db.QueryRowContext(c.Context(), "SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = $1)", id).Scan(&exists); err != nil || !exists {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "workspace not found",
		})
	}

	type statsResponse struct {
		TotalDecisions int     `json:"total_decisions"`
		Blocked        int     `json:"blocked"`
		Allowed        int     `json:"allowed"`
		BlockRate      float64 `json:"block_rate"`
	}

	var stats statsResponse
	err := h.db.QueryRowContext(c.Context(), `
		SELECT
			COUNT(*)::int,
			COUNT(*) FILTER (WHERE decision = 'BLOCK')::int,
			COUNT(*) FILTER (WHERE decision = 'ALLOW')::int
		FROM audit_log WHERE workspace_id = $1
	`, id).Scan(&stats.TotalDecisions, &stats.Blocked, &stats.Allowed)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch stats",
		})
	}

	if stats.TotalDecisions > 0 {
		stats.BlockRate = float64(stats.Blocked) / float64(stats.TotalDecisions)
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"data": stats,
	})
}

// HandleConsume checks if a workspace has remaining analysis quota and
// atomically decrements the counter. Called by Edge services before each
// Engine analysis to enforce plan limits.
//
//	POST /v1/workspaces/:id/consume
//	200: {"allowed": true, "remaining": 999}
//	429: {"allowed": false, "remaining": 0, "retry_after": "..."}
func (h *WorkspaceHandler) HandleConsume(c fiber.Ctx) error {
	id := c.Params("id")

	// Atomic: decrement only if count < limit, then return new values
	var count, limit int
	err := h.db.QueryRowContext(c.Context(), `
		UPDATE workspaces
		SET monthly_analysis_count = monthly_analysis_count + 1,
		    updated_at = NOW()
		WHERE id = $1 AND monthly_analysis_count < monthly_analysis_limit
		RETURNING monthly_analysis_count, monthly_analysis_limit
	`, id).Scan(&count, &limit)
	if err == sql.ErrNoRows {
		// Either workspace not found or limit reached
		var exists bool
		h.db.QueryRowContext(c.Context(), "SELECT EXISTS(SELECT 1 FROM workspaces WHERE id=$1)", id).Scan(&exists)
		if !exists {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "workspace not found"})
		}
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"allowed":    false,
			"remaining":  0,
			"retry_after": "next billing cycle",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "consume failed"})
	}

	remaining := limit - count
	if remaining < 0 {
		remaining = 0
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"allowed":   true,
		"remaining": remaining,
	})
}

// generateAPIKey creates a cryptographically random hex string.
func generateAPIKey(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		// Fallback — rand.Read is practically infallible on modern kernels.
		// Use timestamp to avoid silent empty key.
		buf = []byte(time.Now().UTC().Format(time.RFC3339Nano))
	}
	return hex.EncodeToString(buf)
}
