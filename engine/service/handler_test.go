package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"connectrpc.com/connect"
	"github.com/soyAurelio/AurelioMod/engine/pipeline"
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

// newHandlerDisabled creates a Handler with MIME enforcement disabled.
func newHandlerDisabled(p pipeline.Pipeline) *Handler {
	return &Handler{pipeline: p, enforceMIME: false}
}

// newHandlerEnabled creates a Handler with MIME enforcement enabled.
func newHandlerEnabled(p pipeline.Pipeline) *Handler {
	return &Handler{pipeline: p, enforceMIME: true}
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
	handler := newHandlerDisabled(mock)

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
	handler := newHandlerDisabled(mock)

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
	handler := newHandlerDisabled(mock)

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
	handler := newHandlerDisabled(mock)

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
	handler := newHandlerDisabled(mock)

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
		handler := newHandlerDisabled(mock)

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

// TestHandler_MIMEGate_Disabled verifies that when ENFORCE_MIME is false,
// all uploads pass through regardless of Content-Type.
func TestHandler_MIMEGate_Disabled(t *testing.T) {
	// PE magic bytes with image/jpeg — would be rejected if enforced
	peBytes := []byte{0x4D, 0x5A, 0x90, 0x00}

	mock := &mockPipeline{
		resp: &v1.AnalyzeResponse{
			Decision:    v1.Decision_DECISION_QUEUED,
			ContentHash: "abc",
		},
	}
	handler := newHandlerDisabled(mock)

	req := connect.NewRequest(&v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    peBytes,
		ContentType: v1.ContentType_CONTENT_TYPE_IMAGE,
	})

	resp, err := handler.Analyze(t.Context(), req)
	if err != nil {
		t.Fatalf("MIME disabled should bypass: unexpected error: %v", err)
	}
	if resp.Msg.Decision != v1.Decision_DECISION_QUEUED {
		t.Errorf("Decision = %v, want QUEUED", resp.Msg.Decision)
	}
}

// TestHandler_MIMEGate_Enabled_RejectsPE verifies that with ENFORCE_MIME=true,
// PE bytes claiming to be JPEG are rejected with InvalidArgument.
func TestHandler_MIMEGate_Enabled_RejectsPE(t *testing.T) {
	peBytes := []byte{0x4D, 0x5A, 0x90, 0x00}

	mock := &mockPipeline{}
	handler := newHandlerEnabled(mock)

	req := connect.NewRequest(&v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    peBytes,
		ContentType: v1.ContentType_CONTENT_TYPE_IMAGE,
	})

	_, err := handler.Analyze(t.Context(), req)
	if err == nil {
		t.Fatal("Expected InvalidArgument for PE-as-JPEG with enforce=true, got nil")
	}

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Expected connect.Error, got %T", err)
	}
	if connectErr.Code() != connect.CodeInvalidArgument {
		t.Errorf("Code = %v, want InvalidArgument", connectErr.Code())
	}
	if !strings.Contains(connectErr.Message(), "contradicts") {
		t.Errorf("Message should mention contradiction: %s", connectErr.Message())
	}
}

// TestHandler_MIMEGate_Enabled_AcceptsJPEG verifies that with ENFORCE_MIME=true,
// valid JPEG bytes with image/jpeg Content-Type pass validation.
func TestHandler_MIMEGate_Enabled_AcceptsJPEG(t *testing.T) {
	jpegBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0}

	mock := &mockPipeline{
		resp: &v1.AnalyzeResponse{
			Decision:    v1.Decision_DECISION_BLOCK,
			ContentHash: "def",
		},
	}
	handler := newHandlerEnabled(mock)

	req := connect.NewRequest(&v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    jpegBytes,
		ContentType: v1.ContentType_CONTENT_TYPE_IMAGE,
	})

	resp, err := handler.Analyze(t.Context(), req)
	if err != nil {
		t.Fatalf("JPEG+JPEG should pass MIME gate: %v", err)
	}
	if resp.Msg.Decision != v1.Decision_DECISION_BLOCK {
		t.Errorf("Decision = %v, want BLOCK", resp.Msg.Decision)
	}
}
