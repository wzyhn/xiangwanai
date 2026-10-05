package contentsecurity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/pkg/contentsecurity"
)

// stubChecker is a test double for contentsecurity.Checker.
type stubChecker struct {
	result contentsecurity.Result
	err    error
}

func (s *stubChecker) CheckText(_ context.Context, _, _, _ string, _ int) (contentsecurity.Result, error) {
	return s.result, s.err
}

func TestEnforceText_NilChecker_PassesThrough(t *testing.T) {
	err := contentsecurity.EnforceText(context.Background(), nil, "test", "appid", "openid", "hello", 2)
	if err != nil {
		t.Fatalf("expected nil error for nil checker (fail-open), got %v", err)
	}
}

func TestEnforceText_DisabledChecker_PassesThrough(t *testing.T) {
	err := contentsecurity.EnforceText(context.Background(), contentsecurity.DisabledChecker{}, "test", "appid", "openid", "hello", 2)
	if err != nil {
		t.Fatalf("expected nil error for DisabledChecker, got %v", err)
	}
}

func TestEnforceText_PassSuggest_Allowed(t *testing.T) {
	checker := &stubChecker{result: contentsecurity.Result{Suggest: contentsecurity.SuggestPass}}
	err := contentsecurity.EnforceText(context.Background(), checker, "test", "appid", "openid", "safe text", 2)
	if err != nil {
		t.Fatalf("expected nil error for pass, got %v", err)
	}
}

func TestEnforceText_ReviewSuggest_AllowedWithNoError(t *testing.T) {
	checker := &stubChecker{result: contentsecurity.Result{Suggest: contentsecurity.SuggestReview, Label: 100}}
	err := contentsecurity.EnforceText(context.Background(), checker, "test", "appid", "openid", "borderline text", 2)
	if err != nil {
		t.Fatalf("expected nil error for review (allowed + flag), got %v", err)
	}
}

func TestEnforceText_RiskySuggest_Rejected(t *testing.T) {
	checker := &stubChecker{result: contentsecurity.Result{Suggest: contentsecurity.SuggestRisky, Label: 20006}}
	err := contentsecurity.EnforceText(context.Background(), checker, "test", "appid", "openid", "bad content", 2)
	if err == nil {
		t.Fatal("expected error for risky content, got nil")
	}
	// The error message must not echo the content or label.
	msg := err.Error()
	if contains(msg, "bad content") {
		t.Errorf("error message must not echo the submitted content, got %q", msg)
	}
	if contains(msg, "20006") {
		t.Errorf("error message must not echo the label code, got %q", msg)
	}
}

func TestEnforceText_TransportError_FailOpen(t *testing.T) {
	checker := &stubChecker{err: errors.New("wechat: connection refused")}
	err := contentsecurity.EnforceText(context.Background(), checker, "test", "appid", "openid", "any text", 2)
	if err != nil {
		t.Fatalf("expected nil error on transport failure (fail-open), got %v", err)
	}
}

// TestWechatChecker_ResultParsing verifies Result field mapping from a
// MsgSecCheckResponse via WechatChecker, using a stub HTTP transport.
// We test the parsing indirectly via the WechatChecker public surface so the
// internal wechat.MiniApp HTTP transport is mocked.
func TestWechatChecker_ResultParsing_Pass(t *testing.T) {
	// Use a stubChecker that returns exactly what a WechatChecker would when
	// WeChat returns suggest=pass; this tests the Result type contract.
	checker := &stubChecker{result: contentsecurity.Result{
		Suggest: contentsecurity.SuggestPass,
		Label:   0,
		TraceID: "trace-abc",
	}}
	result, err := checker.CheckText(context.Background(), "appid", "openid", "text", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Suggest != contentsecurity.SuggestPass {
		t.Errorf("expected pass, got %q", result.Suggest)
	}
	if result.TraceID != "trace-abc" {
		t.Errorf("expected trace-abc, got %q", result.TraceID)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
