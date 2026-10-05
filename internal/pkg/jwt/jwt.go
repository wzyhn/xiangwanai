package jwt

import (
	"fmt"
	"strings"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

const defaultSessionTTL = 72 * time.Hour

// clockLeeway tolerates minor clock skew between issuer and verifier (e.g.
// across VMs, NTP drift). 30 s is the industry-standard tolerance and does
// not meaningfully extend the effective lifetime of 72-hour sessions.
// C6: add clock-skew leeway to JWT Parse.
const clockLeeway = 30 * time.Second

// Note (product): refresh-token support is explicitly out of scope for this
// hardening pass. When a long-lived refresh token is added it should use a
// separate signing key and dedicated Claims type so refresh tokens cannot be
// submitted as access tokens.

type Claims struct {
	PrincipalID string `json:"principal_id"`
	Role        string `json:"role"`
	AppID       string `json:"app_id,omitempty"`
	ProductCode string `json:"product_code,omitempty"`
	jwtlib.RegisteredClaims
}

type Manager struct {
	signingSecret    []byte
	verificationKeys jwtlib.VerificationKeySet
}

func NewManager(secret string) *Manager {
	return NewManagerWithPrevious(secret, nil)
}

// NewManagerWithPrevious uses activeSecret for all newly issued tokens and
// accepts previousSecrets only while verifying. This asymmetric contract lets a
// rolling deployment cut signing over first, retain existing sessions for the
// declared compatibility window, and then revoke old keys without ever signing
// new tokens with them.
func NewManagerWithPrevious(activeSecret string, previousSecrets []string) *Manager {
	verification := make([]jwtlib.VerificationKey, 0, 1+len(previousSecrets))
	seen := make(map[string]struct{}, 1+len(previousSecrets))
	verification = append(verification, []byte(activeSecret))
	seen[activeSecret] = struct{}{}
	for _, candidate := range previousSecrets {
		if _, exists := seen[candidate]; exists || candidate == "" {
			continue
		}
		seen[candidate] = struct{}{}
		verification = append(verification, []byte(candidate))
	}
	return &Manager{
		signingSecret:    []byte(activeSecret),
		verificationKeys: jwtlib.VerificationKeySet{Keys: verification},
	}
}

func (m *Manager) Generate(principalID, role string) (string, error) {
	token, _, err := m.GenerateSession(principalID, role)
	return token, err
}

func (m *Manager) GenerateSession(principalID, role string) (string, time.Time, error) {
	return m.GenerateSessionForApp(principalID, role, "")
}

func (m *Manager) GenerateSessionForApp(principalID, role, appID string) (string, time.Time, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(defaultSessionTTL)
	claims := Claims{
		PrincipalID: principalID,
		Role:        role,
		AppID:       appID,
		RegisteredClaims: jwtlib.RegisteredClaims{
			ExpiresAt: jwtlib.NewNumericDate(expiresAt),
			IssuedAt:  jwtlib.NewNumericDate(now),
		},
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.signingSecret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expiresAt, nil
}

// GenerateProductSession issues an access token that cannot be replayed into a
// different product or audience even when deployments temporarily share a
// signing-key rotation window.
func (m *Manager) GenerateProductSession(
	principalID string,
	role string,
	appID string,
	productCode string,
	audience string,
) (string, time.Time, error) {
	if m == nil || len(m.signingSecret) == 0 ||
		!validScopedClaim(principalID) || !validScopedClaim(role) ||
		!validScopedClaim(appID) || !validScopedClaim(productCode) ||
		!validScopedClaim(audience) {
		return "", time.Time{}, fmt.Errorf("invalid product session claims")
	}
	now := time.Now().UTC()
	expiresAt := now.Add(defaultSessionTTL)
	claims := Claims{
		PrincipalID: principalID,
		Role:        role,
		AppID:       appID,
		ProductCode: productCode,
		RegisteredClaims: jwtlib.RegisteredClaims{
			Audience:  jwtlib.ClaimStrings{audience},
			ExpiresAt: jwtlib.NewNumericDate(expiresAt),
			IssuedAt:  jwtlib.NewNumericDate(now),
		},
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.signingSecret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expiresAt, nil
}

func validScopedClaim(value string) bool {
	return value != "" && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, " \t\r\n\x00")
}

// Parse validates tokenStr and returns the embedded Claims.
//
// C5: algorithm is restricted to HS256 via WithValidMethods; the keyfunc also
// asserts *jwtlib.SigningMethodHMAC so a token claiming an asymmetric or
// "none" algorithm is rejected before the secret is compared — defeating the
// classic alg-confusion attack.
//
// C6: WithLeeway(30s) tolerates minor clock skew between issuer and verifier.
func (m *Manager) Parse(tokenStr string) (*Claims, error) {
	token, err := jwtlib.ParseWithClaims(
		tokenStr,
		&Claims{},
		func(t *jwtlib.Token) (any, error) {
			// C5: assert HMAC family before returning the key set.
			if _, ok := t.Method.(*jwtlib.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return m.verificationKeys, nil
		},
		jwtlib.WithValidMethods([]string{"HS256"}), // C5: allowlist
		jwtlib.WithLeeway(clockLeeway),             // C6: clock-skew tolerance
	)
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, jwtlib.ErrTokenInvalidClaims
}
