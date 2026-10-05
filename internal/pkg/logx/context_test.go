package logx

import (
	"context"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/wzyhn/xiangwanai/internal/middleware"
)

// newObservedLogger returns a logger whose entries are captured in an
// observer.ObservedLogs, plus the observer for assertions.
func newObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zap.InfoLevel)
	return zap.New(core), logs
}

func TestFromContext_NilContext_ReturnsNop(t *testing.T) {
	//nolint:staticcheck // intentionally exercising the nil-ctx path
	got := FromContext(nil)
	if got == nil {
		t.Fatal("FromContext(nil) returned nil; expected zap.NewNop()")
	}
	got.Info("should-not-panic")
}

func TestFromContext_NoLoggerStored_ReturnsNop(t *testing.T) {
	got := FromContext(context.Background())
	if got == nil {
		t.Fatal("FromContext(ctx without logger) returned nil")
	}
	got.Info("nop-logger-should-not-panic")
}

func TestWithContext_NilLogger_NoOp(t *testing.T) {
	ctx := WithContext(context.Background(), nil)
	got := FromContext(ctx)
	if got == nil {
		t.Fatal("FromContext after WithContext(nil) returned nil")
	}
}

func TestWithContext_FromContext_RoundTrip(t *testing.T) {
	root, logs := newObservedLogger()
	ctx := WithContext(context.Background(), root)
	got := FromContext(ctx)
	if got == nil {
		t.Fatal("FromContext returned nil")
	}
	got.Info("round-trip")
	entries := logs.AllUntimed()
	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(entries))
	}
	if entries[0].Message != "round-trip" {
		t.Errorf("unexpected message: %q", entries[0].Message)
	}
	for _, f := range entries[0].Context {
		if f.Key == "request_id" {
			t.Errorf("request_id should not be set when none in ctx; got %v", f.String)
		}
	}
}

func TestFromContext_BindsRequestID(t *testing.T) {
	root, logs := newObservedLogger()
	ctx := WithContext(context.Background(), root)
	ctx = middleware.WithRequestID(ctx, "rid-abc-123")

	got := FromContext(ctx)
	got.Info("with-rid")

	entries := logs.AllUntimed()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	var rid string
	for _, f := range entries[0].Context {
		if f.Key == "request_id" {
			rid = f.String
			break
		}
	}
	if rid != "rid-abc-123" {
		t.Errorf("expected request_id=rid-abc-123, got %q", rid)
	}
}

func TestFromContext_NilContext_DoesNotPanicOnRequestID(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("FromContext(nil) panicked: %v", r)
		}
	}()
	//nolint:staticcheck // intentionally testing nil
	_ = FromContext(nil)
}
