// Package weaviate provides the L3 vector search cache layer.
// It connects to Weaviate (vector database) for semantic similarity
// search on previously moderated content.
//
// # Connection
//
// Docker network: weaviate:8080 (compose service name)
// Dev: localhost:8090 (port mapping from compose.yml)
//
// # Schema
//
// Collection: ModeratedContent
// Vector dimension: 768
// Properties: content_hash, workspace_id, decision, category, confidence
package weaviate

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/soyAurelio/AurelioMod/internal/cache"
)

// WeaviateClient performs vector similarity search and indexing
// against the Weaviate vector database.
type WeaviateClient interface {
	// SearchSimilar queries Weaviate for a cached decision with
	// vector similarity > threshold. Returns nil if no match found.
	SearchSimilar(ctx context.Context, contentHash string, threshold float32) (*cache.CachedDecision, error)

	// IndexDecision stores a moderation decision in Weaviate with
	// its vector embedding for future similarity searches.
	IndexDecision(ctx context.Context, contentHash string, decision *cache.CachedDecision) error
}

// HTTPClient implements WeaviateClient using the Weaviate REST API.
// It connects via HTTP (port 8080/8090) for GraphQL queries.
type HTTPClient struct {
	baseURL string
	// client *http.Client // TODO: add when gRPC/REST client is integrated (PR #5)
}

// NewHTTPClient creates a Weaviate HTTP client pointing to the given base URL.
// Example: NewHTTPClient("http://localhost:8090") for dev,
// NewHTTPClient("http://weaviate:8080") for Docker compose.
func NewHTTPClient(baseURL string) *HTTPClient {
	slog.Debug("weaviate client created", "base_url", baseURL)
	return &HTTPClient{baseURL: baseURL}
}

// SearchSimilar performs a nearVector GraphQL query against Weaviate
// looking for decisions with cosine similarity above threshold.
//
// TODO: Full gRPC/GraphQL implementation (PR #5 integration).
// For now, returns a stub indicating no cached match.
func (c *HTTPClient) SearchSimilar(ctx context.Context, contentHash string, threshold float32) (*cache.CachedDecision, error) {
	// Stub: vector search not yet implemented (requires embedding model and
	// weaviate-go-client/v5 dependency). Returns nil (no match) gracefully.
	slog.DebugContext(ctx, "weaviate SearchSimilar: stub (no embedding model yet)",
		"content_hash", contentHash,
		"threshold", threshold,
	)
	return nil, nil
}

// IndexDecision stores a moderation decision with its vector embedding.
//
// TODO: Full gRPC batch insert (PR #5 integration).
// For now, logs and returns nil — cache population is best-effort per spec.
func (c *HTTPClient) IndexDecision(ctx context.Context, contentHash string, decision *cache.CachedDecision) error {
	if decision == nil {
		return fmt.Errorf("weaviate IndexDecision: decision must not be nil")
	}
	slog.DebugContext(ctx, "weaviate IndexDecision: stub (no embedding model yet)",
		"content_hash", contentHash,
		"category", decision.Category,
	)
	return nil
}

// Compile-time interface check.
var _ WeaviateClient = (*HTTPClient)(nil)
