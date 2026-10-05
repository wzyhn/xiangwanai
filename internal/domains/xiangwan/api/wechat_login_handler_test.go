package xiangwanapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity"
	identitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const handlerTestAppID = "wx1234567890abcdef"
const handlerTestPrivacyPolicy = "privacy-v1"

func TestWeChatLoginHandlerReturnsOnlyConsumerSession(t *testing.T) {
	t.Parallel()

	principalID := apiUUID(12)
	expiresAt := time.Date(2026, time.September, 30, 1, 2, 3, 0, time.UTC)
	application := &fakeWeChatLoginApplication{result: identity.LoginResult{
		PrincipalID: principalID,
		IsNew:       true,
		Token: identity.ConsumerToken{
			Value:     "signed.consumer.token",
			ExpiresAt: expiresAt,
		},
	}}
	identityReader := &fakeConsumerIdentityStore{
		nickname:  "享玩用户",
		avatarURL: "/api/v1/xiangwan/avatars/0123456789abcdef0123456789abcdef.png",
	}
	engine := gin.New()
	NewWeChatLoginHandler(
		application,
		handlerTestAppID,
		handlerTestPrivacyPolicy,
		identityReader,
	).RegisterRoutes(engine.Group("/api/v1"))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/wechat/login",
		strings.NewReader(`{"app_id":"`+handlerTestAppID+`","code":"single-use-code"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || application.calls != 1 ||
		application.code != "single-use-code" ||
		!strings.Contains(body, `"principal_id":"`+principalID.String()+`"`) ||
		!strings.Contains(body, `"token":"signed.consumer.token"`) ||
		!strings.Contains(body, `"access_token":"signed.consumer.token"`) ||
		!strings.Contains(body, `"app_id":"`+handlerTestAppID+`"`) ||
		!strings.Contains(body, `"privacy_policy_version":"`+handlerTestPrivacyPolicy+`"`) ||
		!strings.Contains(body, `"profile":{"principal_id":"`+principalID.String()+`"`) ||
		!strings.Contains(body, `"token_type":"Bearer"`) ||
		!strings.Contains(body, `"nickname":"享玩用户"`) ||
		!strings.Contains(body, `"avatar_url":"/api/v1/xiangwan/avatars/0123456789abcdef0123456789abcdef.png"`) ||
		identityReader.readCalls != 1 ||
		!strings.Contains(body, `"is_new":true`) {
		t.Fatalf("POST WeChat Login status=%d app=%+v body=%s", recorder.Code, application, body)
	}
	for _, forbidden := range []string{
		"single-use-code",
		"openid",
		"unionid",
		"session_key",
		"app_secret",
		"signing_key",
	} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("private Login fact %q crossed response: %s", forbidden, body)
		}
	}
}

func TestWeChatLoginHandlerRejectsAmbiguousTransportBeforeExchange(t *testing.T) {
	t.Parallel()

	validBody := `{"app_id":"` + handlerTestAppID + `","code":"single-use-code"}`
	tests := []struct {
		name        string
		path        string
		contentType string
		body        string
		authorize   bool
		operation   bool
		wantStatus  int
	}{
		{name: "query", path: "/api/v1/auth/wechat/login?x=1", contentType: "application/json", body: validBody, wantStatus: http.StatusBadRequest},
		{name: "content type", path: "/api/v1/auth/wechat/login", contentType: "text/plain", body: validBody, wantStatus: http.StatusBadRequest},
		{name: "authorization", path: "/api/v1/auth/wechat/login", contentType: "application/json", body: validBody, authorize: true, wantStatus: http.StatusBadRequest},
		{name: "operation key", path: "/api/v1/auth/wechat/login", contentType: "application/json", body: validBody, operation: true, wantStatus: http.StatusBadRequest},
		{name: "missing code", path: "/api/v1/auth/wechat/login", contentType: "application/json", body: `{"app_id":"` + handlerTestAppID + `"}`, wantStatus: http.StatusBadRequest},
		{name: "missing app id", path: "/api/v1/auth/wechat/login", contentType: "application/json", body: `{"code":"single-use-code"}`, wantStatus: http.StatusBadRequest},
		{name: "wrong app id", path: "/api/v1/auth/wechat/login", contentType: "application/json", body: `{"app_id":"wx0000000000000000","code":"single-use-code"}`, wantStatus: http.StatusBadRequest},
		{name: "padded code", path: "/api/v1/auth/wechat/login", contentType: "application/json", body: `{"app_id":"` + handlerTestAppID + `","code":" padded"}`, wantStatus: http.StatusBadRequest},
		{name: "unknown field", path: "/api/v1/auth/wechat/login", contentType: "application/json", body: `{"app_id":"` + handlerTestAppID + `","code":"single-use-code","product_code":"other"}`, wantStatus: http.StatusBadRequest},
		{name: "trailing json", path: "/api/v1/auth/wechat/login", contentType: "application/json", body: validBody + `{}`, wantStatus: http.StatusBadRequest},
		{name: "oversize", path: "/api/v1/auth/wechat/login", contentType: "application/json", body: `{"code":"` + strings.Repeat("x", maxWeChatLoginBodyBytes) + `"}`, wantStatus: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &fakeWeChatLoginApplication{}
			engine := gin.New()
			NewWeChatLoginHandler(
				application,
				handlerTestAppID,
				handlerTestPrivacyPolicy,
				nil,
			).RegisterRoutes(engine.Group("/api/v1"))
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				test.path,
				strings.NewReader(test.body),
			)
			request.Header.Set("Content-Type", test.contentType)
			if test.authorize {
				request.Header.Set("Authorization", "Bearer old-token")
			}
			if test.operation {
				request.Header.Set("Idempotency-Key", uuid.New().String())
			}
			engine.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || application.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, application.calls, recorder.Body.String())
			}
		})
	}
}

func TestWeChatLoginHandlerMapsOpaqueFailures(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "used code", err: identity.ErrWeChatCodeRejected, wantStatus: http.StatusUnauthorized},
		{name: "provider unavailable", err: identity.ErrWeChatUnavailable, wantStatus: http.StatusServiceUnavailable},
		{name: "inactive account", err: identitypostgres.ErrPrincipalUnavailable, wantStatus: http.StatusForbidden},
		{name: "identity conflict", err: identitypostgres.ErrIdentityConflict, wantStatus: http.StatusConflict},
		{name: "transaction conflict", err: identitypostgres.ErrIdentityTransactionConflict, wantStatus: http.StatusServiceUnavailable},
		{name: "stale generation", err: identitypostgres.ErrIdentityGenerationInactive, wantStatus: http.StatusServiceUnavailable},
		{name: "database failure", err: errors.New("private database address"), wantStatus: http.StatusInternalServerError},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &fakeWeChatLoginApplication{err: test.err}
			engine := gin.New()
			NewWeChatLoginHandler(
				application,
				handlerTestAppID,
				handlerTestPrivacyPolicy,
				nil,
			).RegisterRoutes(engine.Group("/api/v1"))
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/auth/wechat/login",
				strings.NewReader(`{"app_id":"`+handlerTestAppID+`","code":"single-use-code"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus ||
				strings.Contains(recorder.Body.String(), "database address") ||
				strings.Contains(recorder.Body.String(), test.err.Error()) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestWeChatLoginHandlerFailsClosedWhenIdentityReadFails(t *testing.T) {
	t.Parallel()

	application := &fakeWeChatLoginApplication{result: identity.LoginResult{
		PrincipalID: apiUUID(13),
		Token: identity.ConsumerToken{
			Value:     "signed.consumer.token",
			ExpiresAt: time.Now().Add(time.Hour),
		},
	}}
	engine := gin.New()
	NewWeChatLoginHandler(
		application,
		handlerTestAppID,
		handlerTestPrivacyPolicy,
		&fakeConsumerIdentityStore{readErr: identitypostgres.ErrPrincipalUnavailable},
	).RegisterRoutes(engine.Group("/api/v1"))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/wechat/login",
		strings.NewReader(`{"app_id":"`+handlerTestAppID+`","code":"single-use-code"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden ||
		strings.Contains(recorder.Body.String(), "signed.consumer.token") {
		t.Fatalf("identity read failure status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

type fakeWeChatLoginApplication struct {
	result identity.LoginResult
	err    error
	calls  int
	code   string
}

func (application *fakeWeChatLoginApplication) Login(
	_ context.Context,
	code string,
) (identity.LoginResult, error) {
	application.calls++
	application.code = code
	return application.result, application.err
}
