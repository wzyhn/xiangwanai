package logx

import (
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
)

// GinAccessLogger is the structured replacement for the legacy
// internal/middleware.Logger() (stdlib log.Printf). It must be registered
// AFTER middleware.RequestID() so the request_id field is populated on the
// gin.Context by the time the access log line is emitted.
//
// Emitted fields:
//   - method:      HTTP method (GET / POST / ...)
//   - path:        full path with non-sensitive query values; bearer-style
//     query credentials are redacted before logging
//   - status:      response status code
//   - duration_ms: total handler+middleware time as int64 milliseconds. We
//     emit an explicit unit (vs zap.Duration) because zap's default encoders
//     serialize Duration differently in production (seconds, float) vs
//     development (string), which would break log-based latency queries.
//   - request_id:  the X-Request-ID echoed by middleware.RequestID
//   - client_ip:   c.ClientIP() (already proxy-aware via gin's TrustedProxies)
//
// The middleware also stashes the root logger on the request context via
// WithContext, so handlers and downstream modules can retrieve a request-bound
// logger via logx.FromContext(c.Request.Context()) without reaching into gin.
func GinAccessLogger(root *zap.Logger) gin.HandlerFunc {
	if root == nil {
		root = zap.NewNop()
	}
	return func(c *gin.Context) {
		start := time.Now()

		c.Request = c.Request.WithContext(WithContext(c.Request.Context(), root))

		c.Next()

		path := safeRequestPath(c.Request.URL)

		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.Int("status", c.Writer.Status()),
			zap.Int64("duration_ms", time.Since(start).Milliseconds()),
			zap.String("request_id", c.GetString(requestctx.RequestIDHeader)),
			zap.String("client_ip", c.ClientIP()),
		}
		// Errors attached via c.Error() (e.g. response.internalErr's fail-closed
		// 500 fallback) surface here instead of in the client body — this is the
		// only place a non-errx error's real text is persisted, keyed by
		// request_id for the runbook's five-step lookup.
		if len(c.Errors) > 0 {
			msgs := make([]string, len(c.Errors))
			for i, e := range c.Errors {
				msgs[i] = e.Error()
			}
			fields = append(fields, zap.String("errors", strings.Join(msgs, "; ")))
		}
		root.Info("http_access", fields...)
	}
}

var sensitiveQueryKeys = map[string]struct{}{
	"access_token":  {},
	"api_key":       {},
	"authorization": {},
	"client_secret": {},
	"code":          {},
	"credential":    {},
	"id_token":      {},
	"jwt":           {},
	"password":      {},
	"q_ak":          {},
	"refresh_token": {},
	"session_key":   {},
	"js_code":       {},
	"secret":        {},
	"appsecret":     {},
	"sig":           {},
	"signature":     {},
	"token":         {},
}

func isSensitiveQueryKey(key string) bool {
	normalized := strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(strings.TrimSpace(key)))
	if _, sensitive := sensitiveQueryKeys[normalized]; sensitive {
		return true
	}
	for _, suffix := range []string{"_credential", "_token", "_secret", "_password", "_signature"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func safeRequestPath(requestURL *url.URL) string {
	if requestURL == nil {
		return ""
	}
	path := requestURL.Path
	if requestURL.RawQuery == "" {
		return path
	}
	values, err := url.ParseQuery(requestURL.RawQuery)
	if err != nil {
		return path + "?<invalid-query-redacted>"
	}
	for key := range values {
		if isSensitiveQueryKey(key) {
			values[key] = []string{"[REDACTED]"}
		}
	}
	return path + "?" + values.Encode()
}
