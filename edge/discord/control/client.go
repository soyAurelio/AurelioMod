// Package control provides a lightweight HTTP client for the Control API.
// Used by Edge services to check plan quotas before Engine analysis.
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// PlanClient checks workspace quotas via the Control API.
type PlanClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// ConsumeResponse is the JSON returned by POST /v1/workspaces/:id/consume.
type ConsumeResponse struct {
	Allowed   bool   `json:"allowed"`
	Remaining int    `json:"remaining"`
	Error     string `json:"error,omitempty"`
}

// NewPlanClient creates a PlanClient with a PASETO token for auth.
func NewPlanClient(baseURL, token string) *PlanClient {
	return &PlanClient{
		baseURL:    baseURL,
		token:      token,
		httpClient: &http.Client{Timeout: 3 * time.Second},
	}
}

// Consume checks and decrements the workspace monthly analysis quota.
// Returns true if allowed, false if quota exhausted.
// Fails closed: if the Control API is unreachable or unconfigured, analysis
// is denied rather than allowing unbilled consumption.
func (c *PlanClient) Consume(ctx context.Context, workspaceID string) bool {
	if c.baseURL == "" || c.token == "" {
		slog.WarnContext(ctx, "control: PLAN_CLIENT not configured — denying analysis (fail-closed)")
		return false
	}

	url := fmt.Sprintf("%s/v1/workspaces/%s/consume", c.baseURL, workspaceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		slog.WarnContext(ctx, "control: consume request failed", "error", err)
		return false
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "control: consume unavailable", "error", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return false
	}

	if resp.StatusCode != http.StatusOK {
		slog.WarnContext(ctx, "control: consume denied",
			"status", resp.StatusCode,
			"workspace_id", workspaceID,
		)
		return false
	}

	var result ConsumeResponse
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Allowed
}
