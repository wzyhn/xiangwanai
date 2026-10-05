package oidc

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/oauth2"
)

func TestContextWithAdditionalCARejectsNonCertificateBundle(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "not-a-certificate.pem")
	if err := os.WriteFile(path, []byte("not a PEM certificate"), 0o600); err != nil {
		t.Fatalf("write CA fixture: %v", err)
	}
	if _, err := contextWithAdditionalCA(context.Background(), path); err == nil {
		t.Fatal("contextWithAdditionalCA() accepted a non-certificate bundle")
	}
}

func TestClientExchangeCarriesConfiguredHTTPClient(t *testing.T) {
	t.Parallel()

	transport := http.DefaultTransport.(*http.Transport).Clone()
	client := &Client{httpClient: &http.Client{Transport: transport}}
	ctx := client.requestContext(context.Background())
	if got := ctx.Value(oauth2.HTTPClient); got != client.httpClient {
		t.Fatal("request context did not retain configured HTTP client")
	}
}

func TestAuthorizationURLRequestsConfiguredACR(t *testing.T) {
	t.Parallel()

	client := testAuthorizationClient("urn:customer:mfa")
	values := parseAuthorizationQuery(t, client.AuthorizationURL("state", "nonce", "challenge"))
	if got := values.Get("acr_values"); got != "urn:customer:mfa" {
		t.Fatalf("acr_values = %q, want configured ACR", got)
	}
	if got := values.Get("nonce"); got != "nonce" {
		t.Fatalf("nonce = %q, want nonce", got)
	}
	if got := values.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", got)
	}
}

func TestAuthorizationURLOmitsUnconfiguredACR(t *testing.T) {
	t.Parallel()

	values := parseAuthorizationQuery(
		t,
		testAuthorizationClient("").AuthorizationURL("state", "nonce", "challenge"),
	)
	if _, exists := values["acr_values"]; exists {
		t.Fatalf("acr_values unexpectedly present: %q", values.Get("acr_values"))
	}
}

func testAuthorizationClient(requiredACR string) *Client {
	return &Client{
		requiredACR: requiredACR,
		oauth: oauth2.Config{
			ClientID:    "client",
			RedirectURL: "https://admin.example.test/callback",
			Endpoint: oauth2.Endpoint{
				AuthURL: "https://idp.example.test/authorize",
			},
		},
	}
}

func parseAuthorizationQuery(t *testing.T, rawURL string) url.Values {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	return parsed.Query()
}
