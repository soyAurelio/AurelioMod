// Package weaviate provides the L3 vector search cache layer.
// Uses Weaviate's REST API for property-based retrieval of previously
// moderated content. Vector similarity search is deferred to a future
// iteration with CLIP/ResNet embeddings.
//
// Schema: ModeratedContent collection
// Properties: content_hash (unique), decision, category, confidence, workspace_id
//
// Connection:
//   Docker network: weaviate:8080
//   Dev: localhost:8090
package weaviate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
	"github.com/soyAurelio/AurelioMod/internal/cache"
)

// WeaviateClient performs lookup and indexing against Weaviate.
type WeaviateClient interface {
	SearchSimilar(ctx context.Context, contentHash string, threshold float32) (*cache.CachedDecision, error)
	IndexDecision(ctx context.Context, contentHash string, decision *cache.CachedDecision) error
}

// HTTPClient implements WeaviateClient using Weaviate's REST/GraphQL API.
type HTTPClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewHTTPClient creates a Weaviate HTTP client.
func NewHTTPClient(baseURL string) *HTTPClient {
	return &HTTPClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{},
	}
}

// SearchSimilar retrieves a cached decision by content hash.
// Uses GraphQL Get query with where filter on the content_hash property.
func (c *HTTPClient) SearchSimilar(ctx context.Context, contentHash string, threshold float32) (*cache.CachedDecision, error) {
	query := fmt.Sprintf(`{
		Get {
			ModeratedContent(where: {
				path: ["content_hash"],
				operator: Equal,
				valueString: "%s"
			}, limit: 1) {
				content_hash
				decision
				category
				confidence
			}
		}
	}`, contentHash)

	body := map[string]string{"query": query}
	resp, err := c.graphQL(ctx, body)
	if err != nil {
		slog.WarnContext(ctx, "weaviate search failed", "error", err, "content_hash", contentHash)
		return nil, nil
	}

	return parseSearchResult(resp)
}

// IndexDecision stores a moderation decision in Weaviate by content hash.
// Uses the objects REST endpoint with upsert semantics.
func (c *HTTPClient) IndexDecision(ctx context.Context, contentHash string, decision *cache.CachedDecision) error {
	if decision == nil {
		return fmt.Errorf("weaviate IndexDecision: decision must not be nil")
	}

	props := map[string]interface{}{
		"content_hash": contentHash,
		"decision":     decision.Decision.String(),
		"category":     decision.Category,
		"confidence":   decision.Confidence,
	}

	payload := map[string]interface{}{
		"class":      "ModeratedContent",
		"id":         contentHash,
		"properties": props,
	}

	data, _ := json.Marshal(payload)
	url := c.baseURL + "/v1/objects"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("weaviate index request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("weaviate index: %w", err)
	}
	defer resp.Body.Close()

	// 200 or 422 (already exists) are both acceptable
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("weaviate index: HTTP %d: %s", resp.StatusCode, string(body))
	}

	slog.DebugContext(ctx, "weaviate indexed decision",
		"content_hash", contentHash,
		"category", decision.Category,
	)
	return nil
}

// graphQL sends a GraphQL query to Weaviate.
func (c *HTTPClient) graphQL(ctx context.Context, body map[string]string) (map[string]interface{}, error) {
	data, _ := json.Marshal(body)
	url := c.baseURL + "/v1/graphql"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weaviate graphql: HTTP %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("weaviate graphql decode: %w", err)
	}
	return result, nil
}

// parseSearchResult extracts a CachedDecision from a Weaviate GraphQL response.
func parseSearchResult(result map[string]interface{}) (*cache.CachedDecision, error) {
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, nil
	}
	get, ok := data["Get"].(map[string]interface{})
	if !ok {
		return nil, nil
	}
	objects, ok := get["ModeratedContent"].([]interface{})
	if !ok || len(objects) == 0 {
		return nil, nil
	}

	obj := objects[0].(map[string]interface{})
	return &cache.CachedDecision{
		Decision:   decisionFromString(stringField(obj, "decision")),
		Category:   stringField(obj, "category"),
		Confidence: floatField(obj, "confidence"),
	}, nil
}

func stringField(props map[string]interface{}, key string) string {
	v, _ := props[key].(string)
	return v
}

func floatField(props map[string]interface{}, key string) float64 {
	switch v := props[key].(type) {
	case float64:
		return v
	default:
		return 0
	}
}

func decisionFromString(d string) v1.Decision {
	switch d {
	case "DECISION_BLOCK":
		return v1.Decision_DECISION_BLOCK
	case "DECISION_QUEUED":
		return v1.Decision_DECISION_QUEUED
	case "DECISION_ERROR":
		return v1.Decision_DECISION_ERROR
	default:
		return v1.Decision_DECISION_ALLOW
	}
}

var _ WeaviateClient = (*HTTPClient)(nil)
