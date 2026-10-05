package xiangwanruntime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPostgresSecurityThrottleConsumesHashedAtomicWindow(t *testing.T) {
	t.Parallel()

	tenantID := runtimeUUID(90)
	executor := &fakeSecurityThrottleExecutor{count: 7, retryAfter: 41}
	throttle := newPostgresSecurityThrottle(
		tenantID,
		weChatLoginProviderThrottleAction,
		testRuntimeAppID,
		10,
		executor,
	)
	decision, err := throttle.Consume(context.Background())
	if err != nil || !decision.Allowed || decision.Remaining != 3 ||
		decision.RetryAfterSeconds != 41 {
		t.Fatalf("Consume() = %+v, %v", decision, err)
	}
	if executor.calls != 1 ||
		!strings.Contains(executor.query, "ON CONFLICT") ||
		!strings.Contains(executor.query, "FOR UPDATE SKIP LOCKED") ||
		executor.args[0] != tenantID ||
		executor.args[1] != weChatLoginProviderThrottleAction ||
		executor.args[3] != 10 {
		t.Fatalf("executor = %+v", executor)
	}
	digest, ok := executor.args[2].([]byte)
	if !ok || len(digest) != 32 || strings.Contains(string(digest), testRuntimeAppID) {
		t.Fatalf("subject digest was not an opaque SHA-256 value")
	}
}

func TestPostgresSecurityThrottleFailsClosed(t *testing.T) {
	t.Parallel()

	denied := newPostgresSecurityThrottle(
		runtimeUUID(91),
		weChatLoginProviderThrottleAction,
		testRuntimeAppID,
		1,
		&fakeSecurityThrottleExecutor{count: 0, retryAfter: 12},
	)
	decision, err := denied.Consume(context.Background())
	if err != nil || decision.Allowed || decision.Remaining != 0 ||
		decision.RetryAfterSeconds != 12 {
		t.Fatalf("Consume(denied) = %+v, %v", decision, err)
	}

	unavailable := newPostgresSecurityThrottle(
		runtimeUUID(92),
		weChatLoginProviderThrottleAction,
		testRuntimeAppID,
		1,
		&fakeSecurityThrottleExecutor{err: errors.New("database offline")},
	)
	if _, err := unavailable.Consume(context.Background()); !errors.Is(
		err,
		ErrSecurityThrottleUnavailable,
	) || strings.Contains(err.Error(), "database offline") {
		t.Fatalf("Consume(unavailable) error = %v", err)
	}
}

func TestWeChatLoginThrottleSeparatesCallerAndProviderBuckets(t *testing.T) {
	t.Parallel()

	executor := &fakeSecurityThrottleExecutor{count: 1, retryAfter: 30}
	throttle := &PostgresWeChatLoginThrottle{
		tenantID: runtimeUUID(93),
		appID:    testRuntimeAppID,
		executor: executor,
	}
	decision, err := throttle.Consume(context.Background(), "198.51.100.7")
	if err != nil || !decision.Allowed || executor.calls != 2 ||
		len(executor.allArgs) != 2 ||
		executor.allArgs[0][1] != weChatLoginCallerThrottleAction ||
		executor.allArgs[1][1] != weChatLoginProviderThrottleAction {
		t.Fatalf("Consume()=%+v,%v executor=%+v", decision, err, executor)
	}
	callerDigest, callerOK := executor.allArgs[0][2].([]byte)
	providerDigest, providerOK := executor.allArgs[1][2].([]byte)
	if !callerOK || !providerOK || string(callerDigest) == string(providerDigest) ||
		strings.Contains(string(callerDigest), "198.51.100.7") {
		t.Fatalf("caller and provider buckets were not independently hashed")
	}
}

func TestCallerNetworkSubjectDoesNotTrustForwardedHeaders(t *testing.T) {
	t.Parallel()

	var subject string
	engine := gin.New()
	if err := engine.SetTrustedProxies(nil); err != nil {
		t.Fatalf("SetTrustedProxies() error = %v", err)
	}
	engine.POST("/login", func(c *gin.Context) {
		subject = callerNetworkSubject(c)
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	request.RemoteAddr = "198.51.100.8:43100"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	engine.ServeHTTP(httptest.NewRecorder(), request)
	if subject != "198.51.100.8" {
		t.Fatalf("callerNetworkSubject()=%q", subject)
	}
}

func TestCallerNetworkSubjectUsesForwardedClientFromTrustedProxy(t *testing.T) {
	t.Parallel()

	var subject string
	engine := gin.New()
	if err := engine.SetTrustedProxies([]string{"192.0.2.10"}); err != nil {
		t.Fatalf("SetTrustedProxies() error = %v", err)
	}
	engine.POST("/login", func(c *gin.Context) {
		subject = callerNetworkSubject(c)
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	request.RemoteAddr = "192.0.2.10:43100"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	engine.ServeHTTP(httptest.NewRecorder(), request)
	if subject != "203.0.113.9" {
		t.Fatalf("callerNetworkSubject()=%q", subject)
	}
}

func TestSecurityThrottleMiddlewareRejectsBeforeHandler(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		throttle   *fakeSecurityThrottle
		wantStatus int
		wantRetry  string
	}{
		{
			name: "limit",
			throttle: &fakeSecurityThrottle{decision: SecurityThrottleDecision{
				RetryAfterSeconds: 17,
			}},
			wantStatus: http.StatusTooManyRequests,
			wantRetry:  "17",
		},
		{
			name:       "postgres unavailable",
			throttle:   &fakeSecurityThrottle{err: ErrSecurityThrottleUnavailable},
			wantStatus: http.StatusServiceUnavailable,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			handlerCalls := 0
			engine := gin.New()
			engine.POST(
				"/login",
				requireSecurityThrottle(test.throttle),
				func(c *gin.Context) {
					handlerCalls++
					c.Status(http.StatusNoContent)
				},
			)
			request := httptest.NewRequest(http.MethodPost, "/login", nil)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.wantStatus || handlerCalls != 0 ||
				response.Header().Get("Retry-After") != test.wantRetry {
				t.Fatalf(
					"status=%d handler_calls=%d retry_after=%q body=%s",
					response.Code,
					handlerCalls,
					response.Header().Get("Retry-After"),
					response.Body.String(),
				)
			}
		})
	}
}

type fakeSecurityThrottleExecutor struct {
	count      int
	retryAfter int
	err        error

	calls   int
	query   string
	args    []any
	allArgs [][]any
}

func (executor *fakeSecurityThrottleExecutor) queryRowContext(
	_ context.Context,
	query string,
	args ...any,
) securityThrottleRowScanner {
	executor.calls++
	executor.query = query
	executor.args = append([]any(nil), args...)
	executor.allArgs = append(executor.allArgs, append([]any(nil), args...))
	return fakeSecurityThrottleRow{
		count:      executor.count,
		retryAfter: executor.retryAfter,
		err:        executor.err,
	}
}

type fakeSecurityThrottleRow struct {
	count      int
	retryAfter int
	err        error
}

func (row fakeSecurityThrottleRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	*destinations[0].(*int) = row.count
	*destinations[1].(*int) = row.retryAfter
	return nil
}

type fakeSecurityThrottle struct {
	decision SecurityThrottleDecision
	err      error
}

func (fake *fakeSecurityThrottle) Consume(
	context.Context,
	string,
) (SecurityThrottleDecision, error) {
	return fake.decision, fake.err
}
