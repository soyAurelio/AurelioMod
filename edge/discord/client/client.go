// Package client provides a ConnectRPC client wrapper for the Engine
// ContentAnalysisService with circuit breaker resilience. All Analyze calls
// go through a failsafe-go circuit breaker that opens after consecutive
// failures and logs circuit_breaker_open events via slog.
package client

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	connect "connectrpc.com/connect"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"

	aureliomodv1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
	"github.com/soyAurelio/AurelioMod/proto/aureliomod/v1/aureliomodv1connect"
)

// AnalysisClient is the interface for content analysis RPC calls.
// Implementations wrap the ConnectRPC ContentAnalysisServiceClient
// with circuit breaker resilience.
type AnalysisClient interface {
	// Analyze submits content for moderation and returns the decision.
	Analyze(ctx context.Context, req *aureliomodv1.AnalyzeRequest) (*aureliomodv1.AnalyzeResponse, error)
}

// circuitConfig holds the circuit breaker thresholds.
type circuitConfig struct {
	failureThreshold        int
	failureThresholdSeconds int
	openDelay               time.Duration
}

// defaultConfig returns the default circuit breaker configuration:
// 5 failures within 60 seconds, 30-second open delay.
func defaultConfig() circuitConfig {
	return circuitConfig{
		failureThreshold:        5,
		failureThresholdSeconds: 60,
		openDelay:               30 * time.Second,
	}
}

// client implements AnalysisClient wrapping a ConnectRPC client
// with failsafe-go circuit breaker.
type client struct {
	raw    aureliomodv1connect.ContentAnalysisServiceClient
	cb     circuitbreaker.CircuitBreaker[connect.Response[aureliomodv1.AnalyzeResponse]]
	logger *slog.Logger
}

// Compile-time interface check.
var _ AnalysisClient = (*client)(nil)

// NewClient creates an AnalysisClient connected to the Engine at engineURL.
// The returned client uses a circuit breaker (5 failures/60s → 30s open),
// a 5-second HTTP timeout, and logs circuit_breaker_open when the breaker trips.
func NewClient(engineURL string, logger *slog.Logger) AnalysisClient {
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}
	raw := aureliomodv1connect.NewContentAnalysisServiceClient(
		httpClient,
		engineURL,
	)
	return newClientWithConfig(raw, logger, defaultConfig())
}

// newClientWithConfig creates a client with custom circuit breaker config
// (used for testing with shorter delays).
func newClientWithConfig(
	raw aureliomodv1connect.ContentAnalysisServiceClient,
	logger *slog.Logger,
	cfg circuitConfig,
) *client {
	cb := circuitbreaker.NewBuilder[connect.Response[aureliomodv1.AnalyzeResponse]]().
		WithFailureThreshold(uint(cfg.failureThreshold)).
		WithFailureThresholdPeriod(uint(cfg.failureThreshold), time.Duration(cfg.failureThresholdSeconds)*time.Second).
		WithDelay(cfg.openDelay).
		OnOpen(func(event circuitbreaker.StateChangedEvent) {
			logger.WarnContext(context.Background(), "circuit breaker opened",
				slog.String("event", "circuit_breaker_open"),
				slog.String("reason", "consecutive_failures"),
			)
		}).
		OnClose(func(event circuitbreaker.StateChangedEvent) {
			logger.InfoContext(context.Background(), "circuit breaker closed",
				slog.String("event", "circuit_breaker_closed"),
			)
		}).
		OnHalfOpen(func(event circuitbreaker.StateChangedEvent) {
			logger.InfoContext(context.Background(), "circuit breaker half-open",
				slog.String("event", "circuit_breaker_half_open"),
			)
		}).
		Build()

	return &client{
		raw:    raw,
		cb:     cb,
		logger: logger,
	}
}

// Analyze forwards the request to the Engine via ConnectRPC, protected
// by the circuit breaker. If the breaker is open, the call is rejected
// immediately with a circuit breaker error.
func (c *client) Analyze(ctx context.Context, req *aureliomodv1.AnalyzeRequest) (*aureliomodv1.AnalyzeResponse, error) {
	executor := failsafe.With(c.cb)

	result, err := executor.WithContext(ctx).Get(func() (connect.Response[aureliomodv1.AnalyzeResponse], error) {
		connectReq := connect.NewRequest(req)
		resp, err := c.raw.Analyze(ctx, connectReq)
		if err != nil {
			return connect.Response[aureliomodv1.AnalyzeResponse]{}, err
		}
		return *resp, nil
	})
	if err != nil {
		return nil, err
	}
	return result.Msg, nil
}
