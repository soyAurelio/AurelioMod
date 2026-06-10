package controlapi

import (
	"database/sql"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
)

// DecisionsHandler handles decision history queries.
type DecisionsHandler struct {
	db *sql.DB
}

// NewDecisionsHandler creates a DecisionsHandler.
func NewDecisionsHandler(db *sql.DB) *DecisionsHandler {
	return &DecisionsHandler{db: db}
}

// decisionResponse is a single audit log entry exposed via the API.
type decisionResponse struct {
	AuditID               string  `json:"audit_id"`
	ContentHash           string  `json:"content_hash"`
	Decision              string  `json:"decision"`
	Confidence            float64 `json:"confidence,omitempty"`
	Category              string  `json:"category,omitempty"`
	AnalystVersion        string  `json:"analyst_version,omitempty"`
	NormalizationPipeline string  `json:"normalization_pipeline,omitempty"`
	ProcessingMs          int64   `json:"processing_ms"`
	TimestampUTC          string  `json:"timestamp_utc"`
}

// HandleListDecisions returns decision history with pagination and filters.
//
//	GET /v1/workspaces/:id/decisions?status=BLOCK&from=2026-01-01&to=2026-06-01&limit=50&offset=0
//	200: {"data": [...], "total": 1234, "limit": 50, "offset": 0}
func (h *DecisionsHandler) HandleListDecisions(c fiber.Ctx) error {
	workspaceID := c.Params("id")

	// Verify workspace exists
	var exists bool
	if err := h.db.QueryRowContext(c.Context(), "SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = $1)", workspaceID).Scan(&exists); err != nil || !exists {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "workspace not found",
		})
	}

	// Parse filters
	status := c.Query("status", "")
	fromStr := c.Query("from", "")
	toStr := c.Query("to", "")
	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))

	// Clamp limit
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	// Build query with optional filters
	query := `SELECT audit_id, content_hash, decision, COALESCE(confidence, 0),
		COALESCE(category, ''), COALESCE(analyst_version, ''),
		COALESCE(normalization_pipeline, ''), COALESCE(processing_ms, 0),
		timestamp_utc
	FROM audit_log WHERE workspace_id = $1`
	countQuery := `SELECT COUNT(*) FROM audit_log WHERE workspace_id = $1`

	args := []any{workspaceID}
	argIdx := 2

	if status != "" {
		query += ` AND decision = $` + strconv.Itoa(argIdx)
		countQuery += ` AND decision = $` + strconv.Itoa(argIdx)
		args = append(args, status)
		argIdx++
	}
	if fromStr != "" {
		t, err := time.Parse(time.RFC3339, fromStr)
		if err == nil {
			query += ` AND timestamp_utc >= $` + strconv.Itoa(argIdx)
			countQuery += ` AND timestamp_utc >= $` + strconv.Itoa(argIdx)
			args = append(args, t)
			argIdx++
		}
	}
	if toStr != "" {
		t, err := time.Parse(time.RFC3339, toStr)
		if err == nil {
			query += ` AND timestamp_utc <= $` + strconv.Itoa(argIdx)
			countQuery += ` AND timestamp_utc <= $` + strconv.Itoa(argIdx)
			args = append(args, t)
			argIdx++
		}
	}

	// Count total
	var total int
	if err := h.db.QueryRowContext(c.Context(), countQuery, args...).Scan(&total); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to count decisions",
		})
	}

	// Sort by most recent first, with pagination
	query += ` ORDER BY timestamp_utc DESC LIMIT $` + strconv.Itoa(argIdx)
	args = append(args, limit)
	argIdx++
	query += ` OFFSET $` + strconv.Itoa(argIdx)
	args = append(args, offset)

	rows, err := h.db.QueryContext(c.Context(), query, args...)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to query decisions",
		})
	}
	defer rows.Close()

	var decisions []decisionResponse
	for rows.Next() {
		var d decisionResponse
		var ts time.Time
		if err := rows.Scan(&d.AuditID, &d.ContentHash, &d.Decision, &d.Confidence,
			&d.Category, &d.AnalystVersion, &d.NormalizationPipeline,
			&d.ProcessingMs, &ts); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan decision",
			})
		}
		d.TimestampUTC = ts.UTC().Format(time.RFC3339)
		decisions = append(decisions, d)
	}

	if decisions == nil {
		decisions = []decisionResponse{} // always return array, never null
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"data":   decisions,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// HandleGetDecision returns a single decision by audit_id.
//
//	GET /v1/workspaces/:id/decisions/:audit_id
//	200: {"data": {...}}
//	404: {"error": "decision not found"}
func (h *DecisionsHandler) HandleGetDecision(c fiber.Ctx) error {
	workspaceID := c.Params("id")
	auditID := c.Params("audit_id")

	var d decisionResponse
	var ts time.Time
	err := h.db.QueryRowContext(c.Context(), `
		SELECT audit_id, content_hash, decision, COALESCE(confidence, 0),
			COALESCE(category, ''), COALESCE(analyst_version, ''),
			COALESCE(normalization_pipeline, ''), COALESCE(processing_ms, 0),
			timestamp_utc
		FROM audit_log
		WHERE workspace_id = $1 AND audit_id = $2
	`, workspaceID, auditID).Scan(&d.AuditID, &d.ContentHash, &d.Decision,
		&d.Confidence, &d.Category, &d.AnalystVersion,
		&d.NormalizationPipeline, &d.ProcessingMs, &ts)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "decision not found",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch decision",
		})
	}
	d.TimestampUTC = ts.UTC().Format(time.RFC3339)

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"data": d,
	})
}
