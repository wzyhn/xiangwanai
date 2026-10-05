package jwt

import (
	"errors"
	"strings"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

func TestGenerateAndParse(t *testing.T) {
	mgr := NewManager("test-secret")

	token, expiresAt, err := mgr.GenerateSession("pid-123", "student")
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	if token == "" {
		t.Fatal("token is empty")
	}
	if expiresAt.IsZero() {
		t.Fatal("expiresAt is empty")
	}

	claims, err := mgr.Parse(token)
	if err != nil {
		t.Fatalf("failed to parse token: %v", err)
	}
	if claims.PrincipalID != "pid-123" {
		t.Errorf("expected principal_id 'pid-123', got '%s'", claims.PrincipalID)
	}
	if claims.Role != "student" {
		t.Errorf("expected role 'student', got '%s'", claims.Role)
	}
	if claims.ExpiresAt == nil || claims.ExpiresAt.Time.Unix() != expiresAt.Unix() {
		t.Fatalf("expected claims expiry %v, got %+v", expiresAt, claims.ExpiresAt)
	}
}

func TestGenerateProductSessionBindsAppProductAndAudience(t *testing.T) {
	mgr := NewManager("test-secret")
	token, expiresAt, err := mgr.GenerateProductSession(
		"pid-123",
		"consumer",
		"wx-app",
		"xiangwan",
		"xiangwan-consumer",
	)
	if err != nil || token == "" || !expiresAt.After(time.Now()) {
		t.Fatalf("GenerateProductSession() token=%q expiry=%v error=%v", token, expiresAt, err)
	}
	claims, err := mgr.Parse(token)
	if err != nil || claims.AppID != "wx-app" ||
		claims.ProductCode != "xiangwan" || len(claims.Audience) != 1 ||
		claims.Audience[0] != "xiangwan-consumer" {
		t.Fatalf("product claims = %+v, %v", claims, err)
	}
	if _, _, err := mgr.GenerateProductSession(
		"pid-123",
		"consumer",
		"wx-app",
		"xiangwan",
		"two audiences are ambiguous",
	); err == nil {
		t.Fatal("GenerateProductSession() accepted ambiguous audience")
	}
}

func TestParseInvalid(t *testing.T) {
	mgr := NewManager("test-secret")
	_, err := mgr.Parse("invalid-token")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestRotationUsesActiveKeyForSigningAndPreviousKeysOnlyForVerification(t *testing.T) {
	oldManager := NewManager("synthetic-jwt-generation-a")
	oldToken, err := oldManager.Generate("principal-old", "student")
	if err != nil {
		t.Fatalf("generate old token: %v", err)
	}

	overlap := NewManagerWithPrevious(
		"synthetic-jwt-generation-b",
		[]string{"synthetic-jwt-generation-a"},
	)
	if _, err := overlap.Parse(oldToken); err != nil {
		t.Fatalf("compatibility verifier rejected old token: %v", err)
	}
	newToken, err := overlap.Generate("principal-new", "student")
	if err != nil {
		t.Fatalf("generate new token: %v", err)
	}
	if _, err := oldManager.Parse(newToken); err == nil {
		t.Fatal("new token was signed with a previous key")
	}

	revoked := NewManager("synthetic-jwt-generation-b")
	if _, err := revoked.Parse(newToken); err != nil {
		t.Fatalf("active token stopped working after old-key revocation: %v", err)
	}
	if _, err := revoked.Parse(oldToken); err == nil {
		t.Fatal("revoked old key still verified a token")
	}
	outsider, err := NewManager("synthetic-jwt-unauthorized").Generate("principal-outsider", "student")
	if err != nil {
		t.Fatalf("generate outsider token: %v", err)
	}
	if _, err := revoked.Parse(outsider); err == nil {
		t.Fatal("unauthorized key verified a token")
	}
}

func TestRotationPreservesClaimsValidationErrors(t *testing.T) {
	claims := Claims{
		PrincipalID: "principal-expired",
		Role:        "student",
		RegisteredClaims: jwtlib.RegisteredClaims{
			ExpiresAt: jwtlib.NewNumericDate(time.Now().Add(-time.Minute)),
			IssuedAt:  jwtlib.NewNumericDate(time.Now().Add(-time.Hour)),
		},
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("synthetic-jwt-generation-b"))
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}

	manager := NewManagerWithPrevious(
		"synthetic-jwt-generation-b",
		[]string{"synthetic-jwt-generation-a"},
	)
	if _, err := manager.Parse(signed); !errors.Is(err, jwtlib.ErrTokenExpired) {
		t.Fatalf("expected expiration error to survive multi-key verification, got %v", err)
	}
}

// C5: Parse must reject tokens signed with an algorithm other than HS256.
// We craft a token using RS256 (well, jwtlib.SigningMethodRS256 needs a key pair,
// so we craft a structurally valid token that declares alg=RS256 but is signed
// with an HMAC key — the parser must reject it on alg mismatch before even
// reaching the signature check).
//
// The simplest approach: manually build a token with the "none" algorithm header,
// which is the classic CVE. jwtlib v5 already rejects "none" by default, but we
// verify our WithValidMethods check works for a custom alg name too by testing
// that an HS512-signed token (valid HMAC but wrong algorithm) is rejected.
func TestParseRejectsHS512Algorithm(t *testing.T) {
	// Sign a well-formed token with HS512 instead of HS256.
	claims := Claims{
		PrincipalID: "pid-alg-test",
		Role:        "student",
		RegisteredClaims: jwtlib.RegisteredClaims{
			ExpiresAt: jwtlib.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwtlib.NewNumericDate(time.Now()),
		},
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS512, claims)
	signed, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("failed to sign HS512 token: %v", err)
	}

	mgr := NewManager("test-secret")
	_, parseErr := mgr.Parse(signed)
	if parseErr == nil {
		t.Fatal("C5: expected error when parsing HS512-signed token with HS256-only manager, got nil")
	}
}

// C5: Parse must reject a token where alg header has been swapped to "none"
// (the classic alg-confusion / algorithm-bypass attack).
// jwtlib v5 already blocks "none" via its internal check, but our
// WithValidMethods(["HS256"]) provides an independent layer.
func TestParseRejectsNoneAlgorithm(t *testing.T) {
	// Build a raw token with alg=none. jwtlib UnsafeAllowNoneSignatureType would
	// be needed to sign it; instead we just verify our parser rejects a crafted
	// header. We use the "invalid-token" shortcut here — if the token is
	// structurally invalid it still must not yield valid claims.
	mgr := NewManager("test-secret")
	_, err := mgr.Parse("eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJwcmluY2lwYWxfaWQiOiJwaWQtMTIzIiwicm9sZSI6InN0dWRlbnQifQ.")
	if err == nil {
		t.Fatal("C5: expected error for 'none' algorithm token, got nil")
	}
}

// C6: a token issued with its expiry just in the past (within the leeway window)
// must still be considered valid.
func TestParseAcceptsTokenWithinLeeway(t *testing.T) {
	mgr := NewManager("test-secret")

	// Manually craft a token that expired 15 seconds ago (inside the 30s leeway).
	past := time.Now().Add(-15 * time.Second)
	claims := Claims{
		PrincipalID: "pid-leeway",
		Role:        "student",
		RegisteredClaims: jwtlib.RegisteredClaims{
			ExpiresAt: jwtlib.NewNumericDate(past),
			IssuedAt:  jwtlib.NewNumericDate(past.Add(-time.Hour)),
		},
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	parsed, parseErr := mgr.Parse(signed)
	if parseErr != nil {
		t.Fatalf("C6: expected leeway to accept token expired 15s ago, got: %v", parseErr)
	}
	if parsed.PrincipalID != "pid-leeway" {
		t.Errorf("expected principal_id 'pid-leeway', got '%s'", parsed.PrincipalID)
	}
}

// C6: a token expired beyond the leeway (e.g. 60 seconds ago) must still be rejected.
func TestParseRejectsTokenBeyondLeeway(t *testing.T) {
	mgr := NewManager("test-secret")

	past := time.Now().Add(-60 * time.Second)
	claims := Claims{
		PrincipalID: "pid-expired",
		Role:        "student",
		RegisteredClaims: jwtlib.RegisteredClaims{
			ExpiresAt: jwtlib.NewNumericDate(past),
			IssuedAt:  jwtlib.NewNumericDate(past.Add(-time.Hour)),
		},
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	_, parseErr := mgr.Parse(signed)
	if parseErr == nil {
		t.Fatal("C6: expected error for token expired 60s ago (beyond 30s leeway), got nil")
	}
	if !strings.Contains(parseErr.Error(), "token is expired") {
		t.Logf("C6: got error (may be wrapped): %v", parseErr)
	}
}
