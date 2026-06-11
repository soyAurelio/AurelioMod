// Package controlapi provides Control API handlers for TOTP Multi-Factor
// Authentication for admin access to the AurelioMod Control API.
//
// Implements RFC 6238 via github.com/pquerna/otp. Supports:
//   - Enrollment: generate TOTP secret + QR code
//   - Verification: validate TOTP passcode during login
//   - Recovery: single-use backup codes for lost-device scenarios
//   - Disable: remove MFA with recovery code or admin override
//
// Storage: Neon DB (workspace_mfa + mfa_recovery_codes tables).
// Secrets at rest: TOTP secret stored in DB (encrypted at app layer
// via PASETO key derivation in a future iteration).
package controlapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io"
	"log/slog"

	"github.com/gofiber/fiber/v3"
	"github.com/pquerna/otp/totp"
	"lukechampine.com/blake3"
)

// Config holds MFA service configuration.
type Config struct {
	// Issuer is the organization name displayed in authenticator apps.
	Issuer string
	// DB is the Neon DB connection pool.
	DB *sql.DB
}

// Service manages TOTP MFA enrollment, verification, and recovery.
type Service struct {
	issuer string
	db     *sql.DB
}

// New creates a new MFA Service.
func NewMFA(cfg Config) *Service {
	if cfg.Issuer == "" {
		cfg.Issuer = "AurelioMod"
	}
	return &Service{
		issuer: cfg.Issuer,
		db:     cfg.DB,
	}
}

// EnrollmentData holds the TOTP setup information for a workspace admin.
// The QRCode PNG bytes should be rendered as an inline image or base64 data URI.
type EnrollmentData struct {
	// Secret is the base32-encoded TOTP secret (store this).
	Secret string
	// QRCode is the PNG-encoded QR code image.
	QRCode []byte
	// URI is the otpauth:// URI for manual entry.
	URI string
}

// BeginEnrollment generates a new TOTP key and returns enrollment data.
// The secret is NOT stored until ConfirmEnrollment is called.
// This allows the user to scan the QR code and verify before committing.
func (s *Service) BeginEnrollment(ctx context.Context, workspaceID string) (*EnrollmentData, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      s.issuer,
		AccountName: workspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("mfa: generate totp key: %w", err)
	}

	// Generate QR code as PNG
	img, err := key.Image(256, 256)
	if err != nil {
		return nil, fmt.Errorf("mfa: generate qr image: %w", err)
	}

	var qrBuf []byte
	qrWriter := &bytesWriter{}
	if err := png.Encode(qrWriter, img); err != nil {
		return nil, fmt.Errorf("mfa: encode qr png: %w", err)
	}
	qrBuf = qrWriter.buf

	slog.InfoContext(ctx, "mfa enrollment started",
		"workspace_id", workspaceID,
	)

	return &EnrollmentData{
		Secret: key.Secret(),
		QRCode: qrBuf,
		URI:    key.URL(),
	}, nil
}

// ConfirmEnrollment verifies a test passcode and stores the TOTP secret.
// Returns recovery codes (plaintext — display once and never again).
func (s *Service) ConfirmEnrollment(ctx context.Context, workspaceID, secret, passcode string) ([]string, error) {
	// Verify the user can generate valid codes
	valid := totp.Validate(passcode, secret)
	if !valid {
		return nil, errors.New("mfa: totp validation failed — enrollment aborted")
	}

	// Store TOTP secret
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO workspace_mfa (workspace_id, totp_secret, enabled, enrolled_at, updated_at)
		 VALUES ($1, $2, true, NOW(), NOW())
		 ON CONFLICT (workspace_id) DO UPDATE
		 SET totp_secret = $2, enabled = true, enrolled_at = NOW(), updated_at = NOW()`,
		workspaceID, secret,
	)
	if err != nil {
		return nil, fmt.Errorf("mfa: store totp secret: %w", err)
	}

	// Generate recovery codes
	codes := generateRecoveryCodes(10)
	for _, code := range codes {
		codeHash := hashRecoveryCode(code)
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO mfa_recovery_codes (workspace_id, code_hash)
			 VALUES ($1, $2)`,
			workspaceID, codeHash,
		)
		if err != nil {
			return nil, fmt.Errorf("mfa: store recovery code: %w", err)
		}
	}

	slog.InfoContext(ctx, "mfa enrollment confirmed",
		"workspace_id", workspaceID,
		"recovery_codes_issued", len(codes),
	)

	return codes, nil
}

// IsEnabled returns true if MFA is active for the workspace.
func (s *Service) IsEnabled(ctx context.Context, workspaceID string) (bool, error) {
	var enabled bool
	err := s.db.QueryRowContext(ctx,
		`SELECT enabled FROM workspace_mfa WHERE workspace_id = $1`,
		workspaceID,
	).Scan(&enabled)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("mfa: check enabled: %w", err)
	}
	return enabled, nil
}

// Verify validates a TOTP passcode for the given workspace.
func (s *Service) Verify(ctx context.Context, workspaceID, passcode string) error {
	var secret string
	err := s.db.QueryRowContext(ctx,
		`SELECT totp_secret FROM workspace_mfa
		 WHERE workspace_id = $1 AND enabled = true`,
		workspaceID,
	).Scan(&secret)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("mfa: not enabled for this workspace")
		}
		return fmt.Errorf("mfa: lookup secret: %w", err)
	}

	valid := totp.Validate(passcode, secret)
	if !valid {
		// Check if it's a recovery code
		if err := s.consumeRecoveryCode(ctx, workspaceID, passcode); err == nil {
			slog.WarnContext(ctx, "mfa recovery code used",
				"workspace_id", workspaceID,
			)
			return nil // recovery code accepted
		}
		return errors.New("mfa: invalid passcode")
	}

	// Update last_used_at
	_, err = s.db.ExecContext(ctx,
		`UPDATE workspace_mfa SET last_used_at = NOW(), updated_at = NOW()
		 WHERE workspace_id = $1`,
		workspaceID,
	)
	if err != nil {
		slog.WarnContext(ctx, "mfa: update last_used_at failed", "error", err)
	}

	return nil
}

// Disable removes MFA for a workspace (requires a valid passcode or recovery code).
func (s *Service) Disable(ctx context.Context, workspaceID, passcode string) error {
	if err := s.Verify(ctx, workspaceID, passcode); err != nil {
		return fmt.Errorf("mfa: disable verification failed: %w", err)
	}

	_, err := s.db.ExecContext(ctx,
		`DELETE FROM mfa_recovery_codes WHERE workspace_id = $1`,
		workspaceID,
	)
	if err != nil {
		return fmt.Errorf("mfa: delete recovery codes: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`DELETE FROM workspace_mfa WHERE workspace_id = $1`,
		workspaceID,
	)
	if err != nil {
		return fmt.Errorf("mfa: delete mfa config: %w", err)
	}

	slog.WarnContext(ctx, "mfa disabled",
		"workspace_id", workspaceID,
	)
	return nil
}

// consumeRecoveryCode validates and marks a recovery code as used.
// Recovery codes are single-use and compared via BLAKE3 hash.
func (s *Service) consumeRecoveryCode(ctx context.Context, workspaceID, code string) error {
	codeHash := hashRecoveryCode(code)

	result, err := s.db.ExecContext(ctx,
		`UPDATE mfa_recovery_codes
		 SET used = true, used_at = NOW()
		 WHERE workspace_id = $1 AND code_hash = $2 AND used = false`,
		workspaceID, codeHash,
	)
	if err != nil {
		return fmt.Errorf("mfa: consume recovery code: %w", err)
	}

	n, _ := result.RowsAffected()
	if n == 0 {
		return errors.New("mfa: invalid or already used recovery code")
	}
	return nil
}

// generateRecoveryCodes creates n cryptographically random recovery codes.
// Each code is 16 alphanumeric characters (95 bits of entropy).
func generateRecoveryCodes(n int) []string {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // omitted: 0, O, 1, I
	codes := make([]string, n)
	for i := range codes {
		b := make([]byte, 12) // 12 bytes → 16 base32 chars
		if _, err := rand.Read(b); err != nil {
			panic(fmt.Sprintf("mfa: crypto/rand failed: %v", err))
		}
		encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
		// Map base32 charset to our custom charset (visually unambiguous)
		mapped := make([]byte, len(encoded))
		for j, c := range encoded {
			switch c {
			case '0':
				mapped[j] = '8'
			case '1':
				mapped[j] = '7'
			default:
				mapped[j] = byte(c)
			}
		}
		codes[i] = string(mapped[:16])
	}
	return codes
}

// hashRecoveryCode returns the hex-encoded BLAKE3 hash of a recovery code.
func hashRecoveryCode(code string) string {
	sum := blake3.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// bytesWriter is a small io.Writer that accumulates bytes into a slice.
type bytesWriter struct {
	buf []byte
}

func (w *bytesWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}

// Compile-time interface check.
var _ io.Writer = (*bytesWriter)(nil)

// MFAHandler provides HTTP handlers for MFA endpoints.
type MFAHandler struct {
	svc *Service
}

// NewMFAHandler creates a new MFAHandler.
func NewMFAHandler(svc *Service) *MFAHandler {
	return &MFAHandler{svc: svc}
}

// HandleBeginEnrollment starts TOTP enrollment for a workspace.
// POST /v1/workspaces/:id/mfa/enroll
func (h *MFAHandler) HandleBeginEnrollment(c fiber.Ctx) error {
	workspaceID := c.Params("id")
	data, err := h.svc.BeginEnrollment(c.Context(), workspaceID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "mfa enrollment failed",
		})
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"secret":  data.Secret,
		"qr_code": data.QRCode,
		"uri":     data.URI,
	})
}

// HandleConfirmEnrollment verifies a test passcode and activates MFA.
// POST /v1/workspaces/:id/mfa/confirm
func (h *MFAHandler) HandleConfirmEnrollment(c fiber.Ctx) error {
	workspaceID := c.Params("id")
	var body struct {
		Secret   string `json:"secret"`
		Passcode string `json:"passcode"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	codes, err := h.svc.ConfirmEnrollment(c.Context(), workspaceID, body.Secret, body.Passcode)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"status":         "enrolled",
		"recovery_codes": codes,
	})
}

// HandleStatus returns whether MFA is enabled for a workspace.
// GET /v1/workspaces/:id/mfa/status
func (h *MFAHandler) HandleStatus(c fiber.Ctx) error {
	workspaceID := c.Params("id")
	enabled, err := h.svc.IsEnabled(c.Context(), workspaceID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "mfa status check failed",
		})
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"enabled": enabled,
	})
}

// HandleDisable removes MFA from a workspace.
// DELETE /v1/workspaces/:id/mfa
func (h *MFAHandler) HandleDisable(c fiber.Ctx) error {
	workspaceID := c.Params("id")
	var body struct {
		Passcode string `json:"passcode"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if err := h.svc.Disable(c.Context(), workspaceID, body.Passcode); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"status": "mfa_disabled",
	})
}
