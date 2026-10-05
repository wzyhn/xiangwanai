package identitywechat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity"
	"github.com/wzyhn/xiangwanai/internal/pkg/logx"
	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
	wechatpkg "github.com/wzyhn/xiangwanai/internal/pkg/wechat"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const (
	testAppID     = "wx1234567890abcdef"
	testAppSecret = "test" + "-xiangwan-app-secret"
)

func TestExchangerReturnsOnlyStableProviderIdentity(t *testing.T) {
	t.Parallel()

	client := &fakeSessionClient{result: &wechatpkg.SessionResult{
		OpenID:     "openid-1",
		UnionID:    "unionid-1",
		SessionKey: "private-session-key",
	}}
	exchanger, err := newExchanger(client, testAppID, testAppSecret)
	if err != nil {
		t.Fatalf("newExchanger() error = %v", err)
	}
	result, err := exchanger.Exchange(context.Background(), "single-use-code")
	if err != nil || result.OpenID != "openid-1" || result.UnionID != "unionid-1" ||
		client.appID != testAppID || client.appSecret != testAppSecret ||
		client.code != "single-use-code" {
		t.Fatalf("Exchange() = %+v, %v; client=%+v", result, err, client)
	}
	if strings.Contains(result.String(), "session") {
		t.Fatalf("provider projection exposed session material: %s", result.String())
	}
}

func TestExchangerClassifiesCodeAndProviderFailuresWithoutSecrets(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		err     error
		wantErr error
	}{
		{name: "invalid code", err: errors.New("code2session error: 40029 invalid code"), wantErr: identity.ErrWeChatCodeRejected},
		{name: "used code", err: errors.New("code2session error: 40163 code has been used"), wantErr: identity.ErrWeChatCodeRejected},
		{name: "provider unavailable", err: errors.New("request failed for secret=" + testAppSecret), wantErr: identity.ErrWeChatUnavailable},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			exchanger, err := newExchanger(
				&fakeSessionClient{err: test.err},
				testAppID,
				testAppSecret,
			)
			if err != nil {
				t.Fatalf("newExchanger() error = %v", err)
			}
			_, gotErr := exchanger.Exchange(context.Background(), "single-use-code")
			if !errors.Is(gotErr, test.wantErr) ||
				strings.Contains(gotErr.Error(), testAppSecret) {
				t.Fatalf("Exchange() error = %v", gotErr)
			}
		})
	}
}

func TestExchangerRejectsInvalidConfigurationAndProjection(t *testing.T) {
	t.Parallel()

	if _, err := newExchanger(nil, "bad", "short"); !errors.Is(err, ErrInvalidExchanger) {
		t.Fatalf("newExchanger(invalid) error = %v", err)
	}
	exchanger, err := newExchanger(
		&fakeSessionClient{result: &wechatpkg.SessionResult{SessionKey: "private"}},
		testAppID,
		testAppSecret,
	)
	if err != nil {
		t.Fatalf("newExchanger() error = %v", err)
	}
	if _, err := exchanger.Exchange(context.Background(), "single-use-code"); !errors.Is(
		err,
		identity.ErrWeChatUnavailable,
	) {
		t.Fatalf("Exchange(invalid projection) error = %v", err)
	}
}

func TestExchangerRecordsOnlySafeProviderFailureCategory(t *testing.T) {
	t.Parallel()

	core, observed := observer.New(zap.WarnLevel)
	logger := zap.New(core)
	ctx := requestctx.WithRequestID(context.Background(), "request-correlation-id")
	ctx = logx.WithContext(ctx, logger)
	exchanger, err := newExchanger(
		&fakeSessionClient{err: errors.New(
			"wechat code2session error: 40125 invalid appsecret " + testAppSecret,
		)},
		testAppID,
		testAppSecret,
	)
	if err != nil {
		t.Fatalf("newExchanger() error = %v", err)
	}
	_, gotErr := exchanger.Exchange(ctx, "single-use-code")
	if !errors.Is(gotErr, identity.ErrWeChatUnavailable) ||
		identity.WeChatUnavailableCategory(gotErr) !=
			identity.WeChatFailureConfiguration ||
		strings.Contains(gotErr.Error(), testAppSecret) {
		t.Fatalf("Exchange() error = %v", gotErr)
	}
	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("provider failure logs = %+v", entries)
	}
	fields := entries[0].ContextMap()
	if entries[0].Message != "xiangwan_wechat_login_provider_failure" ||
		fields["category"] != string(identity.WeChatFailureConfiguration) ||
		fields["request_id"] != "request-correlation-id" ||
		strings.Contains(entries[0].Message+fmt.Sprint(fields), testAppSecret) {
		t.Fatalf("unsafe provider failure log = %+v", entries[0])
	}
}

type fakeSessionClient struct {
	result *wechatpkg.SessionResult
	err    error

	appID     string
	appSecret string
	code      string
}

func (fake *fakeSessionClient) Code2Session(
	_ context.Context,
	appID string,
	appSecret string,
	code string,
) (*wechatpkg.SessionResult, error) {
	fake.appID = appID
	fake.appSecret = appSecret
	fake.code = code
	return fake.result, fake.err
}
