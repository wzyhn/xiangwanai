package resource

import (
	"errors"
	"testing"
)

func TestExternalDomainPolicyAllowsExactAndExplicitWildcardHTTPS(t *testing.T) {
	t.Parallel()

	policy, err := NewExternalDomainPolicy([]string{
		"docs.example.com",
		"*.media.example.cn",
	})
	if err != nil {
		t.Fatalf("NewExternalDomainPolicy() error = %v", err)
	}
	if !policy.Configured() {
		t.Fatal("non-empty external-domain policy reported unconfigured")
	}
	tests := []struct {
		raw  string
		want string
	}{
		{
			raw:  "https://DOCS.EXAMPLE.COM:443/guide?q=1",
			want: "https://docs.example.com/guide?q=1",
		},
		{
			raw:  "https://cdn.media.example.cn/photo.webp",
			want: "https://cdn.media.example.cn/photo.webp",
		},
	}
	for _, test := range tests {
		got, allowed := policy.AllowURL(test.raw)
		if !allowed || got != test.want {
			t.Fatalf("AllowURL(%q) = %q, %t", test.raw, got, allowed)
		}
	}
}

func TestExternalDomainPolicyRejectsUnsafeOrUnlistedURLs(t *testing.T) {
	t.Parallel()

	policy, err := NewExternalDomainPolicy([]string{
		"docs.example.com",
		"*.media.example.cn",
	})
	if err != nil {
		t.Fatalf("NewExternalDomainPolicy() error = %v", err)
	}
	for _, raw := range []string{
		"http://docs.example.com/guide",
		"https://docs.example.com:8443/guide",
		"https://user@docs.example.com/guide",
		"https://docs.example.com.evil.test/guide",
		"https://media.example.cn/root-is-not-wildcarded",
		"https://127.0.0.1/private",
		"javascript:alert(1)",
		"/relative/path",
		" https://docs.example.com/guide",
		"https://docs.example.com\\@evil.test/guide",
	} {
		if got, allowed := policy.AllowURL(raw); allowed || got != "" {
			t.Fatalf("AllowURL(%q) = %q, %t", raw, got, allowed)
		}
	}
}

func TestExternalDomainPolicyFailsClosedWhenEmpty(t *testing.T) {
	t.Parallel()

	policy, err := NewExternalDomainPolicy(nil)
	if err != nil {
		t.Fatalf("NewExternalDomainPolicy(empty) error = %v", err)
	}
	if policy.Configured() {
		t.Fatal("empty external-domain policy reported configured")
	}
	if _, allowed := policy.AllowURL("https://docs.example.com/guide"); allowed {
		t.Fatal("empty external-domain policy allowed a URL")
	}
}

func TestExternalDomainPolicyRejectsMalformedConfiguration(t *testing.T) {
	t.Parallel()

	for _, domains := range [][]string{
		{"https://docs.example.com"},
		{"example"},
		{"127.0.0.1"},
		{"*.localhost"},
		{"docs.example.com/path"},
		{""},
	} {
		if _, err := NewExternalDomainPolicy(domains); !errors.Is(
			err,
			ErrInvalidExternalDomainPolicy,
		) {
			t.Fatalf("NewExternalDomainPolicy(%q) error = %v", domains, err)
		}
	}
}
