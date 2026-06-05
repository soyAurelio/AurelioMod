package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"connectrpc.com/connect"
	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

// mockPipeline implements pipeline.Pipeline for handler testing.
type mockPipeline struct {
	resp *v1.AnalyzeResponse
	err  error
}

func (m *mockPipeline) Execute(_ context.Context, _ *v1.AnalyzeRequest) (*v1.AnalyzeResponse, error) {
	return m.resp, m.err
}

func TestHandler_ValidRequest_L1Hit(t *testing.T) {
	mock := &mockPipeline{
		resp: &v1.AnalyzeResponse{
			Decision:    v1.Decision_DECISION_BLOCK,
			Confidence:  0.95,
			Category:    "violence_graphic",
			ContentHash: "abc123",
			CacheLevel:  v1.CacheLevel_CACHE_LEVEL_L1_BLAKE3,
		},
	}
	handler := NewHandler(mock)

	req := connect.NewRequest(&v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		ContentId:   "content-1",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})

	resp, err := handler.Analyze(t.Context(), req)
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}

	if resp.Msg.CacheLevel != v1.CacheLevel_CACHE_LEVEL_L1_BLAKE3 {
		t.Errorf("CacheLevel = %v, want L1_BLAKE3", resp.Msg.CacheLevel)
	}
	if resp.Msg.Decision != v1.Decision_DECISION_BLOCK {
		t.Errorf("Decision = %v, want BLOCK", resp.Msg.Decision)
	}
}

func TestHandler_ValidRequest_PipelineMiss(t *testing.T) {
	mock := &mockPipeline{
		resp: &v1.AnalyzeResponse{
			Decision:    v1.Decision_DECISION_QUEUED,
			ContentHash: "def456",
			CacheLevel:  v1.CacheLevel_CACHE_LEVEL_NONE,
		},
	}
	handler := NewHandler(mock)

	req := connect.NewRequest(&v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})

	resp, err := handler.Analyze(t.Context(), req)
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}

	if resp.Msg.CacheLevel != v1.CacheLevel_CACHE_LEVEL_NONE {
		t.Errorf("CacheLevel = %v, want NONE", resp.Msg.CacheLevel)
	}
}

func TestHandler_EmptyWorkspaceId(t *testing.T) {
	mock := &mockPipeline{}
	handler := NewHandler(mock)

	req := connect.NewRequest(&v1.AnalyzeRequest{
		WorkspaceId: "",
		RawBytes:    []byte{0xFF},
	})

	_, err := handler.Analyze(t.Context(), req)
	if err == nil {
		t.Fatal("Expected error for empty workspace_id, got nil")
	}

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Expected connect.Error, got %T", err)
	}
	if connectErr.Code() != connect.CodeInvalidArgument {
		t.Errorf("Code = %v, want InvalidArgument", connectErr.Code())
	}
	if !strings.Contains(connectErr.Message(), "workspace_id") {
		t.Errorf("Message should mention workspace_id, got: %s", connectErr.Message())
	}
}

func TestHandler_EmptyRawBytes(t *testing.T) {
	mock := &mockPipeline{}
	handler := NewHandler(mock)

	req := connect.NewRequest(&v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    nil,
	})

	_, err := handler.Analyze(t.Context(), req)
	if err == nil {
		t.Fatal("Expected error for empty raw_bytes, got nil")
	}

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Expected connect.Error, got %T", err)
	}
	if connectErr.Code() != connect.CodeInvalidArgument {
		t.Errorf("Code = %v, want InvalidArgument", connectErr.Code())
	}
}

func TestHandler_PipelineError(t *testing.T) {
	mock := &mockPipeline{
		err: errors.New("normalization failed"),
	}
	handler := NewHandler(mock)

	req := connect.NewRequest(&v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF},
	})

	_, err := handler.Analyze(t.Context(), req)
	if err == nil {
		t.Fatal("Expected error from pipeline failure, got nil")
	}

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Expected connect.Error, got %T", err)
	}
	if connectErr.Code() != connect.CodeInternal {
		t.Errorf("Code = %v, want Internal", connectErr.Code())
	}
}

func TestHandler_Timeout_DeadlineExceeded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := &mockPipeline{
			err: context.DeadlineExceeded,
		}
		handler := NewHandler(mock)

		req := connect.NewRequest(&v1.AnalyzeRequest{
			WorkspaceId: "ws-test",
			RawBytes:    []byte{0xFF},
		})

		_, err := handler.Analyze(t.Context(), req)
		if err == nil {
			t.Fatal("Expected DeadlineExceeded error, got nil")
		}

		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Fatalf("Expected connect.Error, got %T", err)
		}
		if connectErr.Code() != connect.CodeDeadlineExceeded {
			t.Errorf("Code = %v, want DeadlineExceeded", connectErr.Code())
		}
	})
}
