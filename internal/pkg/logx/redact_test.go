package logx

import (
	"testing"

	"go.uber.org/zap/zapcore"
)

func fieldString(t *testing.T, f zapcore.Field) string {
	t.Helper()
	if f.Type != zapcore.StringType {
		t.Fatalf("expected StringType field, got %v", f.Type)
	}
	return f.String
}

func TestRedactedString_Deterministic(t *testing.T) {
	a := RedactedString("openid", "o_abc123")
	b := RedactedString("openid", "o_abc123")
	if fieldString(t, a) != fieldString(t, b) {
		t.Fatalf("RedactedString not deterministic: %q vs %q", a.String, b.String)
	}
	if a.Key != "openid" {
		t.Fatalf("key = %q, want openid", a.Key)
	}
}

func TestRedactedString_DifferentInputsDifferOutputs(t *testing.T) {
	a := RedactedString("openid", "user-1")
	b := RedactedString("openid", "user-2")
	if fieldString(t, a) == fieldString(t, b) {
		t.Fatalf("expected different hashes for different inputs, both %q", a.String)
	}
}

func TestRedactedString_Empty(t *testing.T) {
	f := RedactedString("openid", "")
	if fieldString(t, f) != "" {
		t.Fatalf("empty input should produce empty string, got %q", f.String)
	}
}

func TestRedactedString_FormatPrefixAndSuffix(t *testing.T) {
	f := RedactedString("openid", "abc")
	s := fieldString(t, f)
	// 8 hex chars + "..." = 11 chars
	if len(s) != redactedHashLen+3 {
		t.Fatalf("output length = %d, want %d (8 hex + '...'); got %q", len(s), redactedHashLen+3, s)
	}
	if s[redactedHashLen:] != "..." {
		t.Fatalf("output suffix = %q, want '...'", s[redactedHashLen:])
	}
	// hex chars only
	for i, r := range s[:redactedHashLen] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			t.Fatalf("non-hex rune %q at idx %d in %q", r, i, s)
		}
	}
}

func TestMaskPhone(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"13912345678", "139****5678"},
		{"abcdefghij", "abc****ghij"},
	}
	for _, tt := range tests {
		f := MaskPhone("phone", tt.in)
		if got := fieldString(t, f); got != tt.want {
			t.Errorf("MaskPhone(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMaskPhone_ShortFallsBackToRedact(t *testing.T) {
	f := MaskPhone("phone", "12345")
	s := fieldString(t, f)
	if len(s) != redactedHashLen+3 {
		t.Fatalf("short phone should fall back to RedactedString, got %q", s)
	}
}

func TestMaskEmail(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"alice@example.com", "a***@example.com"},
		{"x@y.io", "x***@y.io"},
	}
	for _, tt := range tests {
		f := MaskEmail("email", tt.in)
		if got := fieldString(t, f); got != tt.want {
			t.Errorf("MaskEmail(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMaskEmail_MalformedFallsBack(t *testing.T) {
	for _, in := range []string{"no-at-sign", "two@@at", "@noLocal", "trailing@"} {
		f := MaskEmail("email", in)
		s := fieldString(t, f)
		if len(s) != redactedHashLen+3 {
			t.Errorf("MaskEmail(%q) should fall back to redact, got %q", in, s)
		}
	}
}
