// Package oidc implements the customer-controlled OIDC Authorization Code +
// PKCE adapter used by the Xiangwan administrator Web.
package oidc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

var ErrOIDCIdentityRejected = errors.New("xiangwan administrator OIDC identity rejected")

var ErrOIDCDiscoveryUnavailable = errors.New("xiangwan administrator OIDC discovery unavailable")

type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	RequiredACR  string
	CAFile       string
}

type Client struct {
	issuer      string
	requiredACR string
	oauth       oauth2.Config
	verifier    *coreoidc.IDTokenVerifier
	httpClient  *http.Client
}

func New(ctx context.Context, config Config) (*Client, error) {
	if ctx == nil || strings.TrimSpace(config.Issuer) != config.Issuer ||
		config.Issuer == "" || strings.TrimSpace(config.ClientID) != config.ClientID ||
		config.ClientID == "" || strings.TrimSpace(config.ClientSecret) != config.ClientSecret ||
		config.ClientSecret == "" || strings.TrimSpace(config.RedirectURL) != config.RedirectURL ||
		config.RedirectURL == "" || strings.ContainsAny(config.RequiredACR, "\r\n\x00") ||
		strings.TrimSpace(config.CAFile) != config.CAFile ||
		strings.ContainsAny(config.CAFile, "\r\n\x00") {
		return nil, xiangwanadmin.ErrInvalidAdminConfiguration
	}
	discoveryContext, err := contextWithAdditionalCA(ctx, config.CAFile)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOIDCDiscoveryUnavailable, err)
	}
	provider, err := coreoidc.NewProvider(discoveryContext, config.Issuer)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOIDCDiscoveryUnavailable, err)
	}
	trustedHTTPClient, _ := discoveryContext.Value(oauth2.HTTPClient).(*http.Client)
	return &Client{
		issuer:      config.Issuer,
		requiredACR: config.RequiredACR,
		oauth: oauth2.Config{
			ClientID:     config.ClientID,
			ClientSecret: config.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  config.RedirectURL,
			Scopes:       []string{coreoidc.ScopeOpenID},
		},
		verifier:   provider.Verifier(&coreoidc.Config{ClientID: config.ClientID}),
		httpClient: trustedHTTPClient,
	}, nil
}

// contextWithAdditionalCA retains the operating system trust store and only
// appends a deployment-owned CA. It never disables TLS verification, making it
// suitable for customer private PKI as well as local integration environments.
func contextWithAdditionalCA(ctx context.Context, caFile string) (context.Context, error) {
	if caFile == "" {
		return ctx, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read OIDC CA file: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("OIDC CA file contains no PEM certificates")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Transport: transport}), nil
}

func (client *Client) AuthorizationURL(
	state string,
	nonce string,
	pkceChallenge string,
) string {
	if client == nil {
		return ""
	}
	options := []oauth2.AuthCodeOption{
		coreoidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", pkceChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	}
	if client.requiredACR != "" {
		options = append(
			options,
			oauth2.SetAuthURLParam("acr_values", client.requiredACR),
		)
	}
	return client.oauth.AuthCodeURL(state, options...)
}

func (client *Client) Exchange(
	ctx context.Context,
	code string,
	pkceVerifier string,
) (xiangwanadmin.VerifiedIdentity, error) {
	if client == nil || ctx == nil || strings.TrimSpace(code) == "" ||
		strings.TrimSpace(pkceVerifier) == "" {
		return xiangwanadmin.VerifiedIdentity{}, ErrOIDCIdentityRejected
	}
	ctx = client.requestContext(ctx)
	token, err := client.oauth.Exchange(
		ctx,
		code,
		oauth2.VerifierOption(pkceVerifier),
	)
	if err != nil {
		return xiangwanadmin.VerifiedIdentity{}, ErrOIDCIdentityRejected
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return xiangwanadmin.VerifiedIdentity{}, ErrOIDCIdentityRejected
	}
	idToken, err := client.verifier.Verify(ctx, rawIDToken)
	if err != nil || idToken.Issuer != client.issuer || idToken.Subject == "" {
		return xiangwanadmin.VerifiedIdentity{}, ErrOIDCIdentityRejected
	}
	var claims struct {
		Nonce string `json:"nonce"`
		ACR   string `json:"acr"`
	}
	if err := idToken.Claims(&claims); err != nil || claims.Nonce == "" ||
		(client.requiredACR != "" && claims.ACR != client.requiredACR) {
		return xiangwanadmin.VerifiedIdentity{}, ErrOIDCIdentityRejected
	}
	return xiangwanadmin.VerifiedIdentity{
		Issuer: idToken.Issuer, Subject: idToken.Subject,
		Nonce: claims.Nonce, ACR: claims.ACR,
	}, nil
}

func (client *Client) requestContext(ctx context.Context) context.Context {
	if client == nil || client.httpClient == nil {
		return ctx
	}
	// Token exchange and JWKS refresh happen after discovery. Preserve the
	// same system-plus-customer CA trust here; otherwise a private-PKI IdP
	// would discover successfully but fail only after the browser callback.
	return context.WithValue(ctx, oauth2.HTTPClient, client.httpClient)
}
