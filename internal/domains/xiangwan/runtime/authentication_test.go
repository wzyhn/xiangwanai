package xiangwanruntime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	jwtpkg "github.com/wzyhn/xiangwanai/internal/pkg/jwt"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestAuthenticatorAcceptsOnlyBoundCustomerConsumerTokens(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	principalID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	gate := &fakeActivePrincipalGate{}
	authenticator, err := NewAuthenticator(
		testRuntimeSigningKey,
		[]string{testRuntimePreviousKey},
		testRuntimeAppID,
		gate,
	)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	validClaims := runtimeConsumerClaims(principalID, now)
	validToken := signRuntimeConsumerToken(t, validClaims, testRuntimeSigningKey)
	previousToken := signRuntimeConsumerToken(t, validClaims, testRuntimePreviousKey)
	for name, token := range map[string]string{
		"active signing key":        validToken,
		"previous verification key": previousToken,
	} {
		t.Run(name, func(t *testing.T) {
			resolved, resolveErr := authenticator.authenticate(
				[]string{"Bearer " + token},
				now,
			)
			if resolveErr != nil || resolved != principalID {
				t.Fatalf("authenticate() = %s, %v", resolved, resolveErr)
			}
		})
	}

	tests := []struct {
		name          string
		authorization []string
		claims        jwtpkg.Claims
		signingKey    string
	}{
		{name: "missing header"},
		{name: "duplicate header", authorization: []string{"Bearer " + validToken, "Bearer " + validToken}},
		{name: "wrong scheme", authorization: []string{"bearer " + validToken}},
		{name: "extra whitespace", authorization: []string{"Bearer  " + validToken}},
		{name: "wrong customer key", claims: validClaims, signingKey: "synthetic-other-customer-signing-key-for-tests"},
		{name: "wrong app", claims: mutateRuntimeClaims(validClaims, func(claims *jwtpkg.Claims) {
			claims.AppID = "wxabcdef1234567890"
		}), signingKey: testRuntimeSigningKey},
		{name: "missing app", claims: mutateRuntimeClaims(validClaims, func(claims *jwtpkg.Claims) {
			claims.AppID = ""
		}), signingKey: testRuntimeSigningKey},
		{name: "wrong role", claims: mutateRuntimeClaims(validClaims, func(claims *jwtpkg.Claims) {
			claims.Role = "admin"
		}), signingKey: testRuntimeSigningKey},
		{name: "wrong product", claims: mutateRuntimeClaims(validClaims, func(claims *jwtpkg.Claims) {
			claims.ProductCode = "wq-other"
		}), signingKey: testRuntimeSigningKey},
		{name: "wrong audience", claims: mutateRuntimeClaims(validClaims, func(claims *jwtpkg.Claims) {
			claims.Audience = jwtlib.ClaimStrings{"other"}
		}), signingKey: testRuntimeSigningKey},
		{name: "multiple audiences", claims: mutateRuntimeClaims(validClaims, func(claims *jwtpkg.Claims) {
			claims.Audience = jwtlib.ClaimStrings{ConsumerTokenAudience, "other"}
		}), signingKey: testRuntimeSigningKey},
		{name: "noncanonical principal", claims: mutateRuntimeClaims(validClaims, func(claims *jwtpkg.Claims) {
			claims.PrincipalID = strings.ToUpper(principalID.String())
		}), signingKey: testRuntimeSigningKey},
		{name: "missing issued at", claims: mutateRuntimeClaims(validClaims, func(claims *jwtpkg.Claims) {
			claims.IssuedAt = nil
		}), signingKey: testRuntimeSigningKey},
		{name: "excessive lifetime", claims: mutateRuntimeClaims(validClaims, func(claims *jwtpkg.Claims) {
			claims.ExpiresAt = jwtlib.NewNumericDate(now.Add(maximumConsumerTokenTTL + time.Minute))
		}), signingKey: testRuntimeSigningKey},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			authorization := test.authorization
			if authorization == nil && test.signingKey != "" {
				authorization = []string{"Bearer " + signRuntimeConsumerToken(t, test.claims, test.signingKey)}
			}
			if _, gotErr := authenticator.authenticate(authorization, now); !hasErrorCode(gotErr, errx.CodeUnauthorized) {
				t.Fatalf("authenticate() error = %v", gotErr)
			}
		})
	}
}

func TestAuthenticatorMiddlewareChecksLivePrincipalAndOwnsContext(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	principalID := runtimeUUID(71)
	gate := &fakeActivePrincipalGate{}
	authenticator, err := NewAuthenticator(
		testRuntimeSigningKey,
		nil,
		testRuntimeAppID,
		gate,
	)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	token := signRuntimeConsumerToken(
		t,
		runtimeConsumerClaims(principalID, now),
		testRuntimeSigningKey,
	)
	engine := gin.New()
	engine.GET("/protected", authenticator.RequirePrincipal(), func(c *gin.Context) {
		resolved, resolveErr := authenticator.PrincipalID(c)
		if resolveErr != nil {
			response.Err(c, resolveErr)
			return
		}
		response.OK(c, gin.H{"principal_id": resolved.String()})
	})

	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || gate.calls != 1 ||
		gate.principalID != principalID ||
		!strings.Contains(recorder.Body.String(), principalID.String()) {
		t.Fatalf("protected status=%d gate=%+v body=%s", recorder.Code, gate, recorder.Body.String())
	}

	gate.err = principalInactiveError()
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request.Clone(context.Background()))
	if recorder.Code != http.StatusForbidden || gate.calls != 2 ||
		!strings.Contains(recorder.Body.String(), `"code":20003`) {
		t.Fatalf("inactive status=%d gate=%+v body=%s", recorder.Code, gate, recorder.Body.String())
	}
}

func TestAuthenticatorIssuesExactlyScopedConsumerToken(t *testing.T) {
	t.Parallel()

	principalID := runtimeUUID(73)
	authenticator, err := NewAuthenticator(
		testRuntimeSigningKey,
		nil,
		testRuntimeAppID,
		&fakeActivePrincipalGate{},
	)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	issued, err := authenticator.IssueConsumerToken(principalID)
	if err != nil || issued.Value == "" || !issued.ExpiresAt.After(time.Now()) {
		t.Fatalf("IssueConsumerToken() = %+v, %v", issued, err)
	}
	claims, err := authenticator.manager.Parse(issued.Value)
	if err != nil || claims.PrincipalID != principalID.String() ||
		claims.Role != "consumer" || claims.AppID != testRuntimeAppID ||
		claims.ProductCode != ProductCode || len(claims.Audience) != 1 ||
		claims.Audience[0] != ConsumerTokenAudience {
		t.Fatalf("issued claims = %+v, %v", claims, err)
	}
	if _, err := authenticator.IssueConsumerToken(uuid.Nil); !errors.Is(
		err,
		ErrInvalidRuntimeConfig,
	) {
		t.Fatalf("IssueConsumerToken(nil) error = %v", err)
	}
}

func TestPostgresPrincipalGateUsesExactLivePredicate(t *testing.T) {
	t.Parallel()

	principalID := runtimeUUID(72)
	query := &fakePrincipalGateQuery{active: true}
	gate := &PostgresPrincipalGate{query: query}
	if err := gate.RequireActive(context.Background(), principalID); err != nil {
		t.Fatalf("RequireActive(active) error = %v", err)
	}
	if len(query.arguments) != 1 || query.arguments[0] != principalID {
		t.Fatalf("principal query args = %#v", query.arguments)
	}
	for _, fragment := range []string{
		"FROM principals",
		"id = $1",
		"status = 'active'",
		"deleted_at IS NULL",
	} {
		if !strings.Contains(query.statement, fragment) {
			t.Fatalf("principal query missing %q: %s", fragment, query.statement)
		}
	}

	query.active = false
	if err := gate.RequireActive(context.Background(), principalID); !hasErrorCode(err, errx.CodePrincipalInactive) {
		t.Fatalf("RequireActive(inactive) error = %v", err)
	}
	query.err = errors.New("private postgres address")
	if err := gate.RequireActive(context.Background(), principalID); !hasErrorCode(err, errx.CodeInternal) {
		t.Fatalf("RequireActive(database failure) error = %v", err)
	}
}

func runtimeConsumerClaims(principalID uuid.UUID, now time.Time) jwtpkg.Claims {
	return jwtpkg.Claims{
		PrincipalID: principalID.String(),
		Role:        "consumer",
		AppID:       testRuntimeAppID,
		ProductCode: ProductCode,
		RegisteredClaims: jwtlib.RegisteredClaims{
			Audience:  jwtlib.ClaimStrings{ConsumerTokenAudience},
			ExpiresAt: jwtlib.NewNumericDate(now.Add(time.Hour)),
			IssuedAt:  jwtlib.NewNumericDate(now),
		},
	}
}

func mutateRuntimeClaims(
	claims jwtpkg.Claims,
	mutate func(*jwtpkg.Claims),
) jwtpkg.Claims {
	mutate(&claims)
	return claims
}

func signRuntimeConsumerToken(
	t *testing.T,
	claims jwtpkg.Claims,
	signingKey string,
) string {
	t.Helper()
	token, err := jwtlib.NewWithClaims(
		jwtlib.SigningMethodHS256,
		claims,
	).SignedString([]byte(signingKey))
	if err != nil {
		t.Fatalf("sign runtime token: %v", err)
	}
	return token
}

func hasErrorCode(err error, code errx.Code) bool {
	var typed *errx.Error
	return errors.As(err, &typed) && typed.Code == code
}

type fakeActivePrincipalGate struct {
	err error

	calls       int
	principalID uuid.UUID
}

func (gate *fakeActivePrincipalGate) RequireActive(
	_ context.Context,
	principalID uuid.UUID,
) error {
	gate.calls++
	gate.principalID = principalID
	return gate.err
}

type fakePrincipalGateQuery struct {
	active bool
	err    error

	statement string
	arguments []any
}

func (query *fakePrincipalGateQuery) queryRowContext(
	_ context.Context,
	statement string,
	arguments ...any,
) principalGateRow {
	query.statement = statement
	query.arguments = append([]any(nil), arguments...)
	return fakePrincipalGateRow{active: query.active, err: query.err}
}

type fakePrincipalGateRow struct {
	active bool
	err    error
}

func (row fakePrincipalGateRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != 1 {
		return errors.New("unexpected principal gate scan")
	}
	active, ok := destinations[0].(*bool)
	if !ok {
		return errors.New("unexpected principal gate destination")
	}
	*active = row.active
	return nil
}
