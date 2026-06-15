// Package controlapi provides GDPR Art. 22 appeals for automated
// moderation decisions. Users can submit an appeal for any audit_id
// and request human review. Admin updates status (upheld/overturned)
// through the Control API.
package controlapi

import (
	"database/sql"
	"log/slog"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
)

// appealRequest is the user-submitted appeal body.
type appealRequest struct {
	AuditID      string `json:"audit_id"`
	Reason       string `json:"reason"`
	ContactEmail string `json:"contact_email,omitempty"`
}

// appealResponse is the API representation of an appeal.
type appealResponse struct {
	ID            string `json:"id"`
	WorkspaceID   string `json:"workspace_id"`
	AuditID       string `json:"audit_id"`
	Reason        string `json:"reason"`
	ContactEmail  string `json:"contact_email,omitempty"`
	Status        string `json:"status"`
	ReviewerNotes string `json:"reviewer_notes,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// AppealsHandler manages GDPR Art. 22 appeals.
type AppealsHandler struct {
	db *sql.DB
}

// NewAppealsHandler creates an AppealsHandler.
func NewAppealsHandler(db *sql.DB) *AppealsHandler {
	return &AppealsHandler{db: db}
}

// HandleSubmit creates or retrieves an appeal for a specific audit decision.
//
//	POST /v1/workspaces/:id/appeals
//	Body: {"audit_id": "...", "reason": "...", "contact_email": "..."}
//	201: {"data": {...}}
//	200: {"data": {...}} — appeal already exists (idempotent)
//	400: validation error
//	404: audit_id not found for workspace
func (h *AppealsHandler) HandleSubmit(c fiber.Ctx) error {
	workspaceID := c.Params("id")

	var req appealRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if req.AuditID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "audit_id is required",
		})
	}
	if req.Reason == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "reason is required",
		})
	}

	// Verify audit_id belongs to this workspace
	var decisionExists bool
	err := h.db.QueryRowContext(c.Context(),
		`SELECT EXISTS(SELECT 1 FROM audit_log WHERE workspace_id = $1 AND audit_id = $2)`,
		workspaceID, req.AuditID,
	).Scan(&decisionExists)
	if err != nil || !decisionExists {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "decision not found for this workspace",
		})
	}

	// Check if appeal already exists (idempotent)
	var existing appealResponse
	var ts time.Time
	err = h.db.QueryRowContext(c.Context(),
		`SELECT id, workspace_id, audit_id, reason, contact_email,
		 status, reviewer_notes, created_at, updated_at
		 FROM workspace_appeals
		 WHERE workspace_id = $1 AND audit_id = $2`,
		workspaceID, req.AuditID,
	).Scan(&existing.ID, &existing.WorkspaceID, &existing.AuditID,
		&existing.Reason, &existing.ContactEmail, &existing.Status,
		&existing.ReviewerNotes, &ts, &ts)

	if err == nil {
		// Appeal already exists — return it
		existing.CreatedAt = ts.Format(time.RFC3339)
		existing.UpdatedAt = ts.Format(time.RFC3339)
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"data": existing})
	}

	// Create new appeal
	var appeal appealResponse
	var createdAt, updatedAt time.Time
	err = h.db.QueryRowContext(c.Context(),
		`INSERT INTO workspace_appeals (workspace_id, audit_id, reason, contact_email)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, workspace_id, audit_id, reason, contact_email,
		 status, reviewer_notes, created_at, updated_at`,
		workspaceID, req.AuditID, req.Reason, req.ContactEmail,
	).Scan(&appeal.ID, &appeal.WorkspaceID, &appeal.AuditID,
		&appeal.Reason, &appeal.ContactEmail, &appeal.Status,
		&appeal.ReviewerNotes, &createdAt, &updatedAt)
	if err != nil {
		slog.Error("failed to create appeal", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create appeal",
		})
	}
	appeal.CreatedAt = createdAt.Format(time.RFC3339)
	appeal.UpdatedAt = updatedAt.Format(time.RFC3339)

	slog.Info("appeal submitted",
		"workspace_id", workspaceID,
		"audit_id", req.AuditID,
		"appeal_id", appeal.ID,
	)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": appeal})
}

// HandleList returns all appeals for a workspace, newest first.
//
//	GET /v1/workspaces/:id/appeals?status=pending&limit=50&offset=0
//	200: {"data": [...], "total": N}
func (h *AppealsHandler) HandleList(c fiber.Ctx) error {
	workspaceID := c.Params("id")

	// Verify workspace exists
	var exists bool
	if err := h.db.QueryRowContext(c.Context(),
		`SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = $1)`,
		workspaceID,
	).Scan(&exists); err != nil || !exists {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "workspace not found",
		})
	}

	status := c.Query("status", "")
	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `SELECT id, workspace_id, audit_id, reason, contact_email,
		status, reviewer_notes, created_at, updated_at
		FROM workspace_appeals WHERE workspace_id = $1`
	countQuery := `SELECT COUNT(*) FROM workspace_appeals WHERE workspace_id = $1`
	args := []any{workspaceID}
	argIdx := 2

	if status != "" {
		query += ` AND status = $` + itoa(argIdx)
		countQuery += ` AND status = $` + itoa(argIdx)
		args = append(args, status)
		argIdx++
	}

	query += ` ORDER BY created_at DESC LIMIT $` + itoa(argIdx)
	args = append(args, limit)
	argIdx++
	query += ` OFFSET $` + itoa(argIdx)
	args = append(args, offset)

	// Count total
	var total int
	if err := h.db.QueryRowContext(c.Context(), countQuery, args[:len(args)-2]...).Scan(&total); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to count appeals",
		})
	}

	rows, err := h.db.QueryContext(c.Context(), query, args...)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to query appeals",
		})
	}
	defer rows.Close()

	var appeals []appealResponse
	for rows.Next() {
		var a appealResponse
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.AuditID, &a.Reason,
			&a.ContactEmail, &a.Status, &a.ReviewerNotes,
			&createdAt, &updatedAt); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan appeal",
			})
		}
		a.CreatedAt = createdAt.Format(time.RFC3339)
		a.UpdatedAt = updatedAt.Format(time.RFC3339)
		appeals = append(appeals, a)
	}

	if appeals == nil {
		appeals = []appealResponse{}
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"data":   appeals,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// HandleGet returns a single appeal by ID.
//
//	GET /v1/workspaces/:id/appeals/:appeal_id
//	200: {"data": {...}}
//	404: not found
func (h *AppealsHandler) HandleGet(c fiber.Ctx) error {
	workspaceID := c.Params("id")
	appealID := c.Params("appeal_id")

	var a appealResponse
	var createdAt, updatedAt time.Time
	err := h.db.QueryRowContext(c.Context(),
		`SELECT id, workspace_id, audit_id, reason, contact_email,
		 status, reviewer_notes, created_at, updated_at
		 FROM workspace_appeals
		 WHERE workspace_id = $1 AND id = $2`,
		workspaceID, appealID,
	).Scan(&a.ID, &a.WorkspaceID, &a.AuditID, &a.Reason,
		&a.ContactEmail, &a.Status, &a.ReviewerNotes,
		&createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "appeal not found",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch appeal",
		})
	}
	a.CreatedAt = createdAt.Format(time.RFC3339)
	a.UpdatedAt = updatedAt.Format(time.RFC3339)

	return c.Status(fiber.StatusOK).JSON(fiber.Map{"data": a})
}

// itoa is a tiny strconv.Itoa replacement (no import needed).
func itoa(n int) string {
	return fmtInt(n)
}

func fmtInt(n int) string {
	if n < 0 {
		return "-" + fmtUint(uint64(-n))
	}
	return fmtUint(uint64(n))
}

func fmtUint(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
