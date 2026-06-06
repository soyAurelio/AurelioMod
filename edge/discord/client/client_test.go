package client

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	connect "connectrpc.com/connect"
	aureliomodv1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

// stubRPCClient implements aureliomodv1connect.ContentAnalysisServiceClient
// with configurable behavior for testing.
type stubRPCClient struct {
	analyzeFn func(ctx context.Context, req *connect.Request[aureliomodv1.AnalyzeRequest]) (*connect.Response[aureliomodv1.AnalyzeResponse], error)
	callCount int
	lastReq   *aureliomodv1.AnalyzeRequest
}

func (s *stubRPCClient) Analyze(ctx context.Context, req *connect.Request[aureliomodv1.AnalyzeRequest]) (*connect.Response[aureliomodv1.AnalyzeResponse], error) {
	s.callCount++
	s.lastReq = req.Msg
	return s.analyzeFn(ctx, req)
}

// TestClient_Analyze_Success verifies that a successful Analyze call
// delegates to the underlying client and returns the response.
func TestClient_Analyze_Success(t *testing.T) {
	wantResp := &aureliomodv1.AnalyzeResponse{
		Decision:       aureliomodv1.Decision_DECISION_ALLOW,
		Confidence:     0.95,
		ContentHash:    "b3:abc123",
		ProcessingMs:   42,
		AnalystVersion: "wavespeed-v3.2",
	}

	stub := &stubRPCClient{
		analyzeFn: func(ctx context.Context, req *connect.Request[aureliomodv1.AnalyzeRequest]) (*connect.Response[aureliomodv1.AnalyzeResponse], error) {
			return connect.NewResponse(wantResp), nil
		},
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	c := newClientWithConfig(stub, logger, defaultConfig())

	req := &aureliomodv1.AnalyzeRequest{
		WorkspaceId:    "ws_test",
		ContentId:      "msg_001",
		ContentType:    aureliomodv1.ContentType_CONTENT_TYPE_IMAGE,
		SourcePlatform: aureliomodv1.SourcePlatform_SOURCE_PLATFORM_DISCORD,
	}

	resp, err := c.Analyze(t.Context(), req)
	if err != nil {
		t.Fatalf("Analyze() returned error: %v", err)
	}
	if resp.Decision != wantResp.Decision {
		t.Errorf("Decision: got %v, want %v", resp.Decision, wantResp.Decision)
	}
	if resp.ContentHash != wantResp.ContentHash {
		t.Errorf("ContentHash: got %q, want %q", resp.ContentHash, wantResp.ContentHash)
	}
	if stub.callCount != 1 {
		t.Errorf("Expected 1 RPC call, got %d", stub.callCount)
	}
}

// TestClient_Analyze_ErrorPropagation verifies that errors from the
// underlying RPC are propagated to the caller.
func TestClient_Analyze_ErrorPropagation(t *testing.T) {
	rpcErr := errors.New("engine unreachable: connection refused")

	stub := &stubRPCClient{
		analyzeFn: func(ctx context.Context, req *connect.Request[aureliomodv1.AnalyzeRequest]) (*connect.Response[aureliomodv1.AnalyzeResponse], error) {
			return nil, rpcErr
		},
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	c := newClientWithConfig(stub, logger, defaultConfig())

	req := &aureliomodv1.AnalyzeRequest{
		WorkspaceId: "ws_test",
		ContentId:   "msg_002",
	}

	_, err := c.Analyze(t.Context(), req)
	if err == nil {
		t.Fatal("Analyze() should return error when RPC fails")
	}
}

// TestClient_Analyze_CircuitBreakerOpen tests that the circuit breaker
// opens after configured failures and logs circuit_breaker_open.
func TestClient_Analyze_CircuitBreakerOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

		stub := &stubRPCClient{
			analyzeFn: func(ctx context.Context, req *connect.Request[aureliomodv1.AnalyzeRequest]) (*connect.Response[aureliomodv1.AnalyzeResponse], error) {
				return nil, errors.New("engine timeout")
			},
		}

		c := newClientWithConfig(stub, logger, circuitConfig{
			failureThreshold:        3,
			failureThresholdSeconds: 60,
			openDelay:               100 * time.Millisecond,
		})

		req := &aureliomodv1.AnalyzeRequest{
			WorkspaceId: "ws_test",
			ContentId:   "msg_cb",
		}

		// First 3 calls: fail and open the breaker
		for i := 0; i < 3; i++ {
			_, err := c.Analyze(t.Context(), req)
			if err == nil {
				t.Fatalf("Call %d: expected error", i+1)
			}
		}

		// 4th call: breaker should be open → rejected immediately
		_, err := c.Analyze(t.Context(), req)
		if err == nil {
			t.Fatal("4th call should fail with circuit breaker open")
		}

		// Verify circuit_breaker_open was logged
		output := buf.String()
		if !strings.Contains(output, "circuit_breaker_open") {
			t.Errorf("Expected 'circuit_breaker_open' in logs, got: %s", output)
		}
	})
}

// TestClient_Analyze_CircuitBreakerRecovery tests that the breaker
// transitions to half-open and closes again after recovery.
func TestClient_Analyze_CircuitBreakerRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

		failCount := 0
		stub := &stubRPCClient{
			analyzeFn: func(ctx context.Context, req *connect.Request[aureliomodv1.AnalyzeRequest]) (*connect.Response[aureliomodv1.AnalyzeResponse], error) {
				if failCount < 3 {
					failCount++
					return nil, errors.New("engine timeout")
				}
				return connect.NewResponse(&aureliomodv1.AnalyzeResponse{
					Decision:    aureliomodv1.Decision_DECISION_ALLOW,
					ContentHash: "b3:recovered",
				}), nil
			},
		}

		c := newClientWithConfig(stub, logger, circuitConfig{
			failureThreshold:        3,
			failureThresholdSeconds: 60,
			openDelay:               50 * time.Millisecond,
		})

		req := &aureliomodv1.AnalyzeRequest{
			WorkspaceId: "ws_test",
			ContentId:   "msg_recovery",
		}

		// Fail 3 times to open the breaker
		for i := 0; i < 3; i++ {
			_, err := c.Analyze(t.Context(), req)
			if err == nil {
				t.Fatalf("Call %d: expected error", i+1)
			}
		}

		// Wait for circuit breaker delay to expire
		synctest.Wait()
		time.Sleep(100 * time.Millisecond)

		// Next call should succeed (half-open → closed)
		resp, err := c.Analyze(t.Context(), req)
		if err != nil {
			t.Fatalf("After recovery delay, Analyze should succeed, got: %v", err)
		}
		if resp.ContentHash != "b3:recovered" {
			t.Errorf("Unexpected response: %+v", resp)
		}
	})
}

// TestClient_ImplementsAnalysisClient verifies compile-time interface compliance.
func TestClient_ImplementsAnalysisClient(t *testing.T) {
	var _ AnalysisClient = (*client)(nil)
}
