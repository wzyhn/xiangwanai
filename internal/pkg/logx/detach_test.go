package logx

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/wzyhn/xiangwanai/internal/middleware"
)

func TestDetach_PreservesLoggerAndRequestID(t *testing.T) {
	logger := zap.NewExample()
	ctx := WithContext(context.Background(), logger)
	ctx = middleware.WithRequestID(ctx, "rid-detach-1")

	det := Detach(ctx)

	gotLogger, ok := det.Value(loggerCtxKey{}).(*zap.Logger)
	if !ok || gotLogger != logger {
		t.Fatalf("Detach lost logger: ok=%v ptrEq=%v", ok, gotLogger == logger)
	}
	if rid := middleware.RequestIDFromContext(det); rid != "rid-detach-1" {
		t.Fatalf("Detach lost request_id, got %q", rid)
	}
}

func TestDetach_DropsCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	parent = WithContext(parent, zap.NewNop())
	parent = middleware.WithRequestID(parent, "rid-detach-2")

	det := Detach(parent)
	cancel() // parent dead

	if det.Err() != nil {
		t.Fatalf("Detach context should not be cancelled when parent is, got %v", det.Err())
	}
	if _, hasDeadline := det.Deadline(); hasDeadline {
		t.Fatalf("Detach context should not carry a deadline")
	}
	if rid := middleware.RequestIDFromContext(det); rid != "rid-detach-2" {
		t.Fatalf("Detach lost request_id after parent cancel, got %q", rid)
	}
}

func TestDetach_DropsDeadline(t *testing.T) {
	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(1*time.Millisecond))
	defer cancel()
	parent = WithContext(parent, zap.NewNop())

	det := Detach(parent)
	time.Sleep(5 * time.Millisecond)
	if det.Err() != nil {
		t.Fatalf("Detach context should not expire when parent deadline elapses, got %v", det.Err())
	}
}

func TestDetach_NilContext_Safe(t *testing.T) {
	// staticcheck SA1012 disallows literal nil ctx; use typed-nil var to exercise the same defensive path.
	var nilCtx context.Context
	det := Detach(nilCtx)
	if det == nil {
		t.Fatal("Detach(nil) returned nil")
	}
	if det.Err() != nil {
		t.Fatalf("Detach(nil) should be alive, got %v", det.Err())
	}
}

func TestDetach_EmptySource_NoPanic(t *testing.T) {
	det := Detach(context.Background())
	if det == nil {
		t.Fatal("Detach returned nil")
	}
	// FromContext on detached empty ctx returns NopLogger (no panic)
	if got := FromContext(det); got == nil {
		t.Fatal("FromContext returned nil logger")
	}
}
