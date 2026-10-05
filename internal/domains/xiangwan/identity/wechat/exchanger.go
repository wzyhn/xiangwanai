// Package identitywechat adapts the WeChat code2session API without allowing
// provider credentials or session keys to escape the login boundary.
package identitywechat

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity"
	"github.com/wzyhn/xiangwanai/internal/pkg/logx"
	wechatpkg "github.com/wzyhn/xiangwanai/internal/pkg/wechat"
	"go.uber.org/zap"
)

var ErrInvalidExchanger = errors.New("invalid xiangwan WeChat exchanger")

type sessionClient interface {
	Code2Session(
		context.Context,
		string,
		string,
		string,
	) (*wechatpkg.SessionResult, error)
}

type Exchanger struct {
	client    sessionClient
	appID     string
	appSecret string
}

func NewExchanger(appID string, appSecret string) (*Exchanger, error) {
	return NewExchangerWithMiniApp(wechatpkg.NewMiniApp(), appID, appSecret)
}

func NewExchangerWithMiniApp(
	client *wechatpkg.MiniApp,
	appID string,
	appSecret string,
) (*Exchanger, error) {
	return newExchanger(client, appID, appSecret)
}

func newExchanger(
	client sessionClient,
	appID string,
	appSecret string,
) (*Exchanger, error) {
	if client == nil || !validAppID(appID) || !validAppSecret(appSecret) {
		return nil, ErrInvalidExchanger
	}
	return &Exchanger{
		client:    client,
		appID:     appID,
		appSecret: appSecret,
	}, nil
}

func (exchanger *Exchanger) Exchange(
	ctx context.Context,
	code string,
) (identity.ProviderIdentity, error) {
	if exchanger == nil || exchanger.client == nil ||
		!validAppID(exchanger.appID) || !validAppSecret(exchanger.appSecret) ||
		ctx == nil || !identity.ValidWeChatLoginCode(code) {
		return identity.ProviderIdentity{}, ErrInvalidExchanger
	}
	result, err := exchanger.client.Code2Session(
		ctx,
		exchanger.appID,
		exchanger.appSecret,
		code,
	)
	if err != nil {
		if rejectedWeChatCode(err) {
			return identity.ProviderIdentity{}, identity.ErrWeChatCodeRejected
		}
		return identity.ProviderIdentity{}, recordUnavailable(
			ctx,
			classifyUnavailable(err.Error()),
		)
	}
	if result == nil {
		return identity.ProviderIdentity{}, recordUnavailable(
			ctx,
			identity.WeChatFailureMalformedResponse,
		)
	}
	if result.ErrCode != 0 {
		return identity.ProviderIdentity{}, recordUnavailable(
			ctx,
			classifyUnavailable(strconv.Itoa(result.ErrCode)),
		)
	}
	providerIdentity := identity.ProviderIdentity{
		OpenID:  result.OpenID,
		UnionID: result.UnionID,
	}
	if err := identity.ValidateProviderIdentity(providerIdentity); err != nil {
		return identity.ProviderIdentity{}, recordUnavailable(
			ctx,
			identity.WeChatFailureMalformedResponse,
		)
	}
	return providerIdentity, nil
}

func recordUnavailable(
	ctx context.Context,
	category identity.WeChatFailureCategory,
) error {
	logx.FromContext(ctx).Warn(
		"xiangwan_wechat_login_provider_failure",
		zap.String("category", string(category)),
	)
	return identity.NewWeChatUnavailableFailure(category)
}

func classifyUnavailable(value string) identity.WeChatFailureCategory {
	message := strings.ToLower(value)
	switch {
	case containsUnavailableMarker(
		message,
		"40125",
		"40013",
		"invalid appsecret",
		"invalid appid",
	):
		return identity.WeChatFailureConfiguration
	case containsUnavailableMarker(
		message,
		"45011",
		"frequency limit",
		"minute-quota",
		"system busy",
	):
		return identity.WeChatFailureProviderThrottled
	case containsUnavailableMarker(
		message,
		"timeout",
		"deadline exceeded",
		"request failed",
		"read body failed",
		"connection reset",
	):
		return identity.WeChatFailureTransport
	case containsUnavailableMarker(
		message,
		"unmarshal failed",
		"malformed",
	):
		return identity.WeChatFailureMalformedResponse
	default:
		return identity.WeChatFailureProvider
	}
}

func containsUnavailableMarker(value string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func rejectedWeChatCode(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"40029",
		"40163",
		"61453",
		"invalid code",
		"invalid js_code",
		"code has been used",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func validAppID(value string) bool {
	if len(value) < 3 || len(value) > 64 || !strings.HasPrefix(value, "wx") {
		return false
	}
	for _, character := range value[2:] {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func validAppSecret(value string) bool {
	return len(value) >= 16 && len(value) <= 128 &&
		value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, " \t\r\n\x00")
}
