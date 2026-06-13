// Package weaviate provides the L3 vector search cache layer using Weaviate.
// Content decisions are indexed with a perceptual embedding vector (pHash bits
// converted to 64-dim float32). Similarity search uses nearVector with cosine
// distance, enabling detection of visually similar content beyond exact hash
// matches (L1 BLAKE3) and Hamming-distance matches (L2 pHash).
//
// Schema: ModeratedContent collection
// Properties: content_hash (unique), decision, category, confidence, workspace_id
//
// Connection:
//
//	Docker network: weaviate:8080
//	Dev: localhost:8090
package weaviate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/soyAurelio/AurelioMod/internal/cache"
	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

// WeaviateClient performs lookup and indexing against Weaviate.
type WeaviateClient interface {
	// SearchSimilar finds cached decisions with visually similar content
	// using nearVector search. vector is a float32 embedding (e.g.,
	// pHash bits converted to 64-dim vector). threshold is the minimum
	// cosine similarity (0.0-1.0) required for a match.
	SearchSimilar(ctx context.Context, vector []float32, threshold float32) (*cache.CachedDecision, error)

	// IndexDecision stores a moderation decision with its embedding vector
	// in Weaviate for future similarity searches.
	IndexDecision(ctx context.Context, contentHash string, vector []float32, decision *cache.CachedDecision) error
}

// HTTPClient implements WeaviateClient using Weaviate's REST/GraphQL API.
type HTTPClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewHTTPClient creates a Weaviate HTTP client.
// Auto-prepends https:// if the addr has no scheme.
func NewHTTPClient(addr string) *HTTPClient {
	if !strings.Contains(addr, "://") {
		addr = "https://" + addr
	}
	return &HTTPClient{
		baseURL:    strings.TrimRight(addr, "/"),
		httpClient: &http.Client{},
	}
}

// SearchSimilar retrieves a cached decision by nearVector similarity search.
// Uses Weaviate GraphQL nearVector query with cosine distance.
func (c *HTTPClient) SearchSimilar(ctx context.Context, vector []float32, threshold float32) (*cache.CachedDecision, error) {
	// Build nearVector GraphQL query with inline vector
	vecStr := float32VectorToString(vector)
	query := fmt.Sprintf(`{
		Get {
			ModeratedContent(
				nearVector: {
					vector: %s,
					certainty: %f
				}
				limit: 1
			) {
				content_hash
				decision
				category
				confidence
			}
		}
	}`, vecStr, threshold)

	body := map[string]string{"query": query}
	resp, err := c.graphQL(ctx, body)
	if err != nil {
		slog.WarnContext(ctx, "weaviate nearVector search failed", "error", err)
		return nil, nil
	}

	return parseSearchResult(resp)
}

// IndexDecision stores a moderation decision in Weaviate with its embedding vector.
// Uses the objects REST endpoint with upsert semantics. The vector enables
// nearVector similarity search for visually similar content.
func (c *HTTPClient) IndexDecision(ctx context.Context, contentHash string, vector []float32, decision *cache.CachedDecision) error {
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
		"vector":     vector,
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

// float32VectorToString formats a float32 slice as a JSON array string
// for inline use in Weaviate GraphQL nearVector queries.
func float32VectorToString(vec []float32) string {
	if len(vec) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

var _ WeaviateClient = (*HTTPClient)(nil)
