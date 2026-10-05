package xiangwanruntime

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	xiangwanapi "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/api"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	jwtpkg "github.com/wzyhn/xiangwanai/internal/pkg/jwt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	ConsumerTokenAudience   = "wq-xiangwan-consumer"
	principalIDContextKey   = "xiangwan.runtime.principal_id"
	maximumConsumerTokenTTL = 72 * time.Hour
	consumerTokenClockSkew  = 30 * time.Second
)

const activePrincipalQuery = `
SELECT EXISTS (
    SELECT 1
    FROM principals
    WHERE id = $1
      AND status = 'active'
      AND deleted_at IS NULL
)
`

type ActivePrincipalGate interface {
	RequireActive(context.Context, uuid.UUID) error
}

type principalGateRow interface {
	Scan(...any) error
}

type principalGateQuery interface {
	queryRowContext(context.Context, string, ...any) principalGateRow
}

type principalGateDatabase interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type sqlPrincipalGateQuery struct {
	database principalGateDatabase
}

func (query sqlPrincipalGateQuery) queryRowContext(
	ctx context.Context,
	statement string,
	arguments ...any,
) principalGateRow {
	return query.database.QueryRowContext(ctx, statement, arguments...)
}

type PostgresPrincipalGate struct {
	query principalGateQuery
}

func NewPostgresPrincipalGate(
	database principalGateDatabase,
) *PostgresPrincipalGate {
	if database == nil {
		return nil
	}
	return &PostgresPrincipalGate{
		query: sqlPrincipalGateQuery{database: database},
	}
}

func (gate *PostgresPrincipalGate) RequireActive(
	ctx context.Context,
	principalID uuid.UUID,
) error {
	if gate == nil || gate.query == nil || ctx == nil || principalID == uuid.Nil {
		return principalInactiveError()
	}
	var active bool
	if err := gate.query.queryRowContext(
		ctx,
		activePrincipalQuery,
		principalID,
	).Scan(&active); err != nil {
		return errors.Join(
			errx.NewInternal("failed to check principal status"),
			err,
		)
	}
	if !active {
		return principalInactiveError()
	}
	return nil
}

func principalInactiveError() *errx.Error {
	return errx.New(errx.CodePrincipalInactive, "principal is inactive")
}

type Authenticator struct {
	manager *jwtpkg.Manager
	appID   string
	gate    ActivePrincipalGate
}

func NewAuthenticator(
	signingKey string,
	previousSecrets []string,
	appID string,
	gate ActivePrincipalGate,
) (*Authenticator, error) {
	if !validSigningKey(signingKey) || !validWechatAppID(appID) || gate == nil ||
		len(previousSecrets) > maximumPreviousKeys {
		return nil, ErrInvalidRuntimeConfig
	}
	seen := map[string]struct{}{signingKey: {}}
	for _, candidate := range previousSecrets {
		if !validSigningKey(candidate) {
			return nil, ErrInvalidRuntimeConfig
		}
		if _, duplicate := seen[candidate]; duplicate {
			return nil, ErrInvalidRuntimeConfig
		}
		seen[candidate] = struct{}{}
	}
	return &Authenticator{
		manager: jwtpkg.NewManagerWithPrevious(signingKey, previousSecrets),
		appID:   appID,
		gate:    gate,
	}, nil
}

func (authenticator *Authenticator) RequirePrincipal() gin.HandlerFunc {
	return func(c *gin.Context) {
		principalID, err := authenticator.authenticate(
			c.Request.Header.Values("Authorization"),
			time.Now().UTC(),
		)
		if err != nil {
			xiangwanapi.WriteCustomerError(c, err)
			c.Abort()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), readinessTimeout)
		defer cancel()
		if err := authenticator.gate.RequireActive(ctx, principalID); err != nil {
			xiangwanapi.WriteCustomerError(c, err)
			c.Abort()
			return
		}
		c.Set(principalIDContextKey, principalID)
		c.Next()
	}
}

func (authenticator *Authenticator) PrincipalID(
	c *gin.Context,
) (uuid.UUID, error) {
	if authenticator == nil || c == nil {
		return uuid.Nil, errx.NewUnauthorized("missing authenticated principal")
	}
	value, exists := c.Get(principalIDContextKey)
	principalID, valid := value.(uuid.UUID)
	if !exists || !valid || principalID == uuid.Nil {
		return uuid.Nil, errx.NewUnauthorized("missing authenticated principal")
	}
	return principalID, nil
}

func (authenticator *Authenticator) IssueConsumerToken(
	principalID uuid.UUID,
) (identity.ConsumerToken, error) {
	if authenticator == nil || authenticator.manager == nil ||
		principalID == uuid.Nil || !validWechatAppID(authenticator.appID) {
		return identity.ConsumerToken{}, ErrInvalidRuntimeConfig
	}
	token, expiresAt, err := authenticator.manager.GenerateProductSession(
		principalID.String(),
		"consumer",
		authenticator.appID,
		ProductCode,
		ConsumerTokenAudience,
	)
	if err != nil {
		return identity.ConsumerToken{}, ErrInvalidRuntimeConfig
	}
	return identity.ConsumerToken{Value: token, ExpiresAt: expiresAt}, nil
}

func (authenticator *Authenticator) authenticate(
	authorization []string,
	now time.Time,
) (uuid.UUID, error) {
	if authenticator == nil || authenticator.manager == nil ||
		!validWechatAppID(authenticator.appID) || authenticator.gate == nil ||
		len(authorization) != 1 {
		return uuid.Nil, errx.NewUnauthorized("invalid authorization")
	}
	header := authorization[0]
	scheme, token, found := strings.Cut(header, " ")
	if !found || scheme != "Bearer" || token == "" ||
		token != strings.TrimSpace(token) || strings.ContainsAny(token, " \t\r\n\x00") {
		return uuid.Nil, errx.NewUnauthorized("invalid authorization")
	}
	claims, err := authenticator.manager.Parse(token)
	if err != nil || claims == nil {
		return uuid.Nil, errx.NewUnauthorized("invalid token")
	}
	principalID, err := uuid.Parse(claims.PrincipalID)
	if err != nil || principalID == uuid.Nil ||
		principalID.String() != claims.PrincipalID ||
		claims.AppID != authenticator.appID ||
		claims.Role != "consumer" ||
		claims.ProductCode != ProductCode ||
		len(claims.Audience) != 1 ||
		claims.Audience[0] != ConsumerTokenAudience ||
		!validConsumerTokenLifetime(claims, now) {
		return uuid.Nil, errx.NewUnauthorized("invalid token")
	}
	return principalID, nil
}

func validConsumerTokenLifetime(claims *jwtpkg.Claims, now time.Time) bool {
	if claims == nil || claims.IssuedAt == nil || claims.ExpiresAt == nil ||
		now.IsZero() {
		return false
	}
	issuedAt := claims.IssuedAt.Time.UTC()
	expiresAt := claims.ExpiresAt.Time.UTC()
	return expiresAt.After(issuedAt) &&
		expiresAt.Sub(issuedAt) <= maximumConsumerTokenTTL &&
		!issuedAt.After(now.UTC().Add(consumerTokenClockSkew))
}
