package requestctx

import (
	"context"
	"testing"
)

func TestRequestIDRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := WithRequestID(context.Background(), "request-1")
	if got := RequestID(ctx); got != "request-1" {
		t.Fatalf("RequestID() = %q, want request-1", got)
	}
}

func TestRequestIDHandlesNilContext(t *testing.T) {
	t.Parallel()

	if got := RequestID(nil); got != "" { //nolint:staticcheck // Defensive nil-context contract.
		t.Fatalf("RequestID(nil) = %q, want empty", got)
	}
	if got := RequestID(WithRequestID(nil, "request-2")); got != "request-2" { //nolint:staticcheck // Defensive nil-context contract.
		t.Fatalf("RequestID(WithRequestID(nil)) = %q, want request-2", got)
	}
}
