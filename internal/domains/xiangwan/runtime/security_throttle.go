package xiangwanruntime

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	weChatLoginCallerThrottleAction   = "wechat_login_caller"
	weChatLoginProviderThrottleAction = "wechat_login_provider_exchange"
	defaultWeChatLoginCallerLimit     = 10
	defaultWeChatLoginProviderLimit   = 120
)

var (
	ErrInvalidSecurityThrottle = errors.New(
		"invalid xiangwan security throttle",
	)
	ErrSecurityThrottleUnavailable = errors.New(
		"xiangwan security throttle is unavailable",
	)
)

type SecurityThrottleDecision struct {
	Allowed           bool
	Remaining         int
	RetryAfterSeconds int
}

type securityThrottleRowScanner interface {
	Scan(destinations ...any) error
}

type securityThrottleQueryExecutor interface {
	queryRowContext(
		context.Context,
		string,
		...any,
	) securityThrottleRowScanner
}

type sqlSecurityThrottleExecutor struct {
	database *sql.DB
}

func (executor sqlSecurityThrottleExecutor) queryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) securityThrottleRowScanner {
	return executor.database.QueryRowContext(ctx, query, args...)
}

// PostgresSecurityThrottle owns one action/channel bucket. PostgreSQL time and
// one atomic upsert make the decision consistent across Runtime replicas.
// subjectHash is derived locally and the raw AppID/channel identifier is never
// persisted.
type PostgresSecurityThrottle struct {
	tenantID    uuid.UUID
	action      string
	subjectHash [sha256.Size]byte
	limit       int
	executor    securityThrottleQueryExecutor
}

func NewPostgresWeChatLoginThrottle(
	database *sql.DB,
	tenantID uuid.UUID,
	appID string,
) *PostgresWeChatLoginThrottle {
	if database == nil {
		return &PostgresWeChatLoginThrottle{}
	}
	return &PostgresWeChatLoginThrottle{
		tenantID: tenantID,
		appID:    appID,
		executor: sqlSecurityThrottleExecutor{database: database},
	}
}

// PostgresWeChatLoginThrottle enforces a small caller-specific budget before
// consuming the independent AppID-wide provider quota. One anonymous caller
// therefore cannot exhaust the shared provider bucket by itself.
type PostgresWeChatLoginThrottle struct {
	tenantID uuid.UUID
	appID    string
	executor securityThrottleQueryExecutor
}

func (throttle *PostgresWeChatLoginThrottle) Consume(
	ctx context.Context,
	callerSubject string,
) (SecurityThrottleDecision, error) {
	if throttle == nil || throttle.tenantID == uuid.Nil ||
		!validWechatAppID(throttle.appID) || throttle.executor == nil ||
		ctx == nil || !validCallerSubject(callerSubject) {
		return SecurityThrottleDecision{}, ErrInvalidSecurityThrottle
	}
	caller := newPostgresSecurityThrottle(
		throttle.tenantID,
		weChatLoginCallerThrottleAction,
		throttle.appID+"\x00"+callerSubject,
		defaultWeChatLoginCallerLimit,
		throttle.executor,
	)
	callerDecision, err := caller.Consume(ctx)
	if err != nil || !callerDecision.Allowed {
		return callerDecision, err
	}
	provider := newPostgresSecurityThrottle(
		throttle.tenantID,
		weChatLoginProviderThrottleAction,
		throttle.appID,
		defaultWeChatLoginProviderLimit,
		throttle.executor,
	)
	providerDecision, err := provider.Consume(ctx)
	if err != nil || !providerDecision.Allowed {
		return providerDecision, err
	}
	return callerDecision, nil
}

func newPostgresSecurityThrottle(
	tenantID uuid.UUID,
	action string,
	subject string,
	limit int,
	executor securityThrottleQueryExecutor,
) *PostgresSecurityThrottle {
	hash := sha256.Sum256([]byte(
		ProductCode + "\x00" + tenantID.String() + "\x00" + action + "\x00" + subject,
	))
	return &PostgresSecurityThrottle{
		tenantID:    tenantID,
		action:      action,
		subjectHash: hash,
		limit:       limit,
		executor:    executor,
	}
}

const consumeSecurityThrottleQuery = `
WITH current_window AS (
    SELECT to_timestamp(
        floor(EXTRACT(EPOCH FROM clock_timestamp()) / 60) * 60
    ) AS window_start
), expired AS (
    SELECT throttle.ctid
    FROM xiangwan_security_throttles AS throttle
    WHERE throttle.expires_at <= clock_timestamp()
    ORDER BY throttle.expires_at
    LIMIT 32
    FOR UPDATE SKIP LOCKED
), pruned AS (
    DELETE FROM xiangwan_security_throttles AS throttle
    USING expired
    WHERE throttle.ctid = expired.ctid
), consumed AS (
    INSERT INTO xiangwan_security_throttles (
        scope_key,
        tenant_id,
        action,
        subject_hash,
        window_start,
        request_count,
        expires_at,
        updated_at
    )
    SELECT
        'wq-xiangwan',
        $1,
        $2,
        $3,
        current_window.window_start,
        1,
        current_window.window_start + INTERVAL '10 minutes',
        clock_timestamp()
    FROM current_window
    ON CONFLICT (scope_key, action, subject_hash, window_start)
    DO UPDATE SET
        request_count = xiangwan_security_throttles.request_count + 1,
        expires_at = GREATEST(
            xiangwan_security_throttles.expires_at,
            EXCLUDED.expires_at
        ),
        updated_at = clock_timestamp()
    WHERE xiangwan_security_throttles.request_count < $4
    RETURNING request_count
)
SELECT
    COALESCE((SELECT request_count FROM consumed), 0),
    GREATEST(
        1,
        CEIL(EXTRACT(EPOCH FROM (
            current_window.window_start + INTERVAL '1 minute' - clock_timestamp()
        )))::INTEGER
    )
FROM current_window
`

func (throttle *PostgresSecurityThrottle) Consume(
	ctx context.Context,
) (SecurityThrottleDecision, error) {
	if throttle == nil || throttle.tenantID == uuid.Nil ||
		throttle.executor == nil || throttle.limit < 1 ||
		throttle.limit > 1_000_000 ||
		!validSecurityThrottleAction(throttle.action) || ctx == nil {
		return SecurityThrottleDecision{}, ErrInvalidSecurityThrottle
	}
	var count int
	var retryAfter int
	if err := throttle.executor.queryRowContext(
		ctx,
		consumeSecurityThrottleQuery,
		throttle.tenantID,
		throttle.action,
		throttle.subjectHash[:],
		throttle.limit,
	).Scan(&count, &retryAfter); err != nil {
		return SecurityThrottleDecision{}, fmt.Errorf(
			"consume xiangwan security throttle: %w",
			ErrSecurityThrottleUnavailable,
		)
	}
	if retryAfter < 1 {
		retryAfter = 1
	}
	remaining := throttle.limit - count
	if remaining < 0 || count == 0 {
		remaining = 0
	}
	return SecurityThrottleDecision{
		Allowed:           count > 0,
		Remaining:         remaining,
		RetryAfterSeconds: retryAfter,
	}, nil
}

type securityThrottle interface {
	Consume(context.Context, string) (SecurityThrottleDecision, error)
}

func requireSecurityThrottle(throttle securityThrottle) gin.HandlerFunc {
	return func(c *gin.Context) {
		if throttle == nil {
			writeServiceUnavailable(c)
			c.Abort()
			return
		}
		decision, err := throttle.Consume(
			c.Request.Context(),
			callerNetworkSubject(c),
		)
		if err != nil {
			writeServiceUnavailable(c)
			c.Abort()
			return
		}
		if !decision.Allowed {
			c.Header("Retry-After", strconv.Itoa(decision.RetryAfterSeconds))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, response.Body{
				Code:    42900,
				Message: "rate limit exceeded",
			})
			return
		}
		c.Header("X-RateLimit-Remaining", strconv.Itoa(decision.Remaining))
		c.Next()
	}
}

func callerNetworkSubject(c *gin.Context) string {
	if c == nil {
		return "unknown"
	}
	value := strings.TrimSpace(c.ClientIP())
	if !validCallerSubject(value) {
		return "unknown"
	}
	return strings.ToLower(value)
}

func validCallerSubject(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 255 &&
		!strings.ContainsAny(value, "\r\n\x00")
}

func validSecurityThrottleAction(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if index == 0 {
			if character < 'a' || character > 'z' {
				return false
			}
			continue
		}
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			!strings.ContainsRune("_.:-", character) {
			return false
		}
	}
	return true
}
