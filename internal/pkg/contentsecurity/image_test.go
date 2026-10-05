package contentsecurity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/pkg/contentsecurity"
)

// stubImageModerator is a test double for contentsecurity.ImageModerator.
type stubImageModerator struct {
	result contentsecurity.MediaCheckResult
	err    error
	calls  int
}

func (s *stubImageModerator) CheckImageAsync(_ context.Context, _, _, _ string, _ int) (contentsecurity.MediaCheckResult, error) {
	s.calls++
	return s.result, s.err
}

func TestDisabledImageModerator_NoOp(t *testing.T) {
	res, err := contentsecurity.DisabledImageModerator{}.CheckImageAsync(
		context.Background(), "appid", "openid", "https://example.com/a.jpg", 2,
	)
	if err != nil {
		t.Fatalf("disabled moderator must not error, got %v", err)
	}
	if res.TraceID != "" {
		t.Errorf("disabled moderator must return empty TraceID (fail-open), got %q", res.TraceID)
	}
}

func TestImageModerator_Enqueued_ReturnsTraceID(t *testing.T) {
	mod := &stubImageModerator{result: contentsecurity.MediaCheckResult{TraceID: "trace-img-1"}}
	res, err := mod.CheckImageAsync(context.Background(), "appid", "openid", "https://example.com/a.jpg", 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TraceID != "trace-img-1" {
		t.Errorf("expected trace-img-1, got %q", res.TraceID)
	}
	if mod.calls != 1 {
		t.Errorf("expected 1 enqueue call, got %d", mod.calls)
	}
}

func TestImageModerator_TransportError_Propagated(t *testing.T) {
	// The capability itself surfaces transport errors; the FAIL-OPEN decision is
	// the caller's (orchestration layer), mirroring EnforceText's contract.
	mod := &stubImageModerator{err: errors.New("wechat: connection refused")}
	_, err := mod.CheckImageAsync(context.Background(), "appid", "openid", "https://example.com/a.jpg", 4)
	if err == nil {
		t.Fatal("expected transport error to be surfaced for the caller to fail-open on")
	}
}

func TestImageModerator_RejectedJob_EmptyTraceID(t *testing.T) {
	// WeChat accepted the request but did not return a trace_id → treat as no job.
	mod := &stubImageModerator{result: contentsecurity.MediaCheckResult{TraceID: ""}}
	res, err := mod.CheckImageAsync(context.Background(), "appid", "openid", "https://example.com/a.jpg", 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TraceID != "" {
		t.Errorf("expected empty TraceID, got %q", res.TraceID)
	}
}
