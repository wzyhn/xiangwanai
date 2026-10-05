// Package identity owns Xiangwan consumer login identity. WeChat codes and
// session keys are transient provider credentials; PostgreSQL owns only the
// stable provider-to-Principal binding and no Redis session is introduced.
package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const MaxWeChatLoginCodeBytes = 256

var (
	ErrInvalidLoginService     = errors.New("invalid xiangwan Login service")
	ErrInvalidLoginRequest     = errors.New("invalid xiangwan Login request")
	ErrInvalidProviderIdentity = errors.New("invalid xiangwan provider identity")
	ErrWeChatCodeRejected      = errors.New("xiangwan WeChat login code rejected")
	ErrWeChatUnavailable       = errors.New("xiangwan WeChat login unavailable")
	ErrInvalidLoginResult      = errors.New("invalid xiangwan Login result")
)

type WeChatFailureCategory string

const (
	WeChatFailureConfiguration     WeChatFailureCategory = "configuration"
	WeChatFailureProviderThrottled WeChatFailureCategory = "provider_throttled"
	WeChatFailureTransport         WeChatFailureCategory = "transport"
	WeChatFailureMalformedResponse WeChatFailureCategory = "malformed_response"
	WeChatFailureProvider          WeChatFailureCategory = "provider"
)

type WeChatUnavailableFailure struct {
	category WeChatFailureCategory
}

func NewWeChatUnavailableFailure(category WeChatFailureCategory) error {
	if !validWeChatFailureCategory(category) {
		category = WeChatFailureProvider
	}
	return &WeChatUnavailableFailure{category: category}
}

func (failure *WeChatUnavailableFailure) Error() string {
	if failure == nil {
		return ErrWeChatUnavailable.Error()
	}
	return ErrWeChatUnavailable.Error() + ": " + string(failure.category)
}

func (*WeChatUnavailableFailure) Unwrap() error {
	return ErrWeChatUnavailable
}

func WeChatUnavailableCategory(err error) WeChatFailureCategory {
	var failure *WeChatUnavailableFailure
	if !errors.As(err, &failure) || failure == nil ||
		!validWeChatFailureCategory(failure.category) {
		return WeChatFailureProvider
	}
	return failure.category
}

func validWeChatFailureCategory(category WeChatFailureCategory) bool {
	switch category {
	case WeChatFailureConfiguration,
		WeChatFailureProviderThrottled,
		WeChatFailureTransport,
		WeChatFailureMalformedResponse,
		WeChatFailureProvider:
		return true
	default:
		return false
	}
}

type ProviderIdentity struct {
	OpenID  string
	UnionID string
}

type ResolvedPrincipal struct {
	ID    uuid.UUID
	IsNew bool
}

type ConsumerToken struct {
	Value     string
	ExpiresAt time.Time
}

type LoginResult struct {
	PrincipalID uuid.UUID
	IsNew       bool
	Token       ConsumerToken
}

type CodeExchanger interface {
	Exchange(context.Context, string) (ProviderIdentity, error)
}

type PrincipalResolver interface {
	Resolve(
		context.Context,
		ProviderIdentity,
		time.Time,
	) (ResolvedPrincipal, error)
}

type ConsumerTokenIssuer interface {
	IssueConsumerToken(uuid.UUID) (ConsumerToken, error)
}

type LoginService struct {
	exchanger CodeExchanger
	resolver  PrincipalResolver
	issuer    ConsumerTokenIssuer
	now       func() time.Time
}

func NewLoginService(
	exchanger CodeExchanger,
	resolver PrincipalResolver,
	issuer ConsumerTokenIssuer,
	now func() time.Time,
) (*LoginService, error) {
	if exchanger == nil || resolver == nil || issuer == nil || now == nil {
		return nil, ErrInvalidLoginService
	}
	return &LoginService{
		exchanger: exchanger,
		resolver:  resolver,
		issuer:    issuer,
		now:       now,
	}, nil
}

func (service *LoginService) Login(
	ctx context.Context,
	code string,
) (LoginResult, error) {
	if service == nil || service.exchanger == nil || service.resolver == nil ||
		service.issuer == nil || service.now == nil {
		return LoginResult{}, ErrInvalidLoginService
	}
	if ctx == nil || !ValidWeChatLoginCode(code) {
		return LoginResult{}, ErrInvalidLoginRequest
	}
	providerIdentity, err := service.exchanger.Exchange(ctx, code)
	if err != nil {
		return LoginResult{}, fmt.Errorf("exchange xiangwan WeChat code: %w", err)
	}
	if err := ValidateProviderIdentity(providerIdentity); err != nil {
		return LoginResult{}, ErrInvalidLoginResult
	}
	now := service.now().UTC().Truncate(time.Microsecond)
	if now.IsZero() {
		return LoginResult{}, ErrInvalidLoginService
	}
	principal, err := service.resolver.Resolve(ctx, providerIdentity, now)
	if err != nil {
		return LoginResult{}, fmt.Errorf("resolve xiangwan consumer identity: %w", err)
	}
	if principal.ID == uuid.Nil {
		return LoginResult{}, ErrInvalidLoginResult
	}
	token, err := service.issuer.IssueConsumerToken(principal.ID)
	if err != nil {
		return LoginResult{}, fmt.Errorf("issue xiangwan consumer token: %w", err)
	}
	if !validConsumerToken(token, now) {
		return LoginResult{}, ErrInvalidLoginResult
	}
	return LoginResult{
		PrincipalID: principal.ID,
		IsNew:       principal.IsNew,
		Token:       token,
	}, nil
}

func ValidWeChatLoginCode(value string) bool {
	if value == "" || value != strings.TrimSpace(value) ||
		len(value) > MaxWeChatLoginCodeBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func ValidateProviderIdentity(value ProviderIdentity) error {
	if !validProviderSubject(value.OpenID) ||
		(value.UnionID != "" && !validProviderSubject(value.UnionID)) {
		return ErrInvalidProviderIdentity
	}
	return nil
}

func validProviderSubject(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 128 ||
		!utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validConsumerToken(value ConsumerToken, issuedAfter time.Time) bool {
	return value.Value != "" && value.Value == strings.TrimSpace(value.Value) &&
		!strings.ContainsAny(value.Value, " \t\r\n\x00") &&
		value.ExpiresAt.After(issuedAfter)
}

func (ProviderIdentity) String() string {
	return "xiangwan ProviderIdentity{subject:[REDACTED]}"
}

func (value ProviderIdentity) GoString() string {
	return value.String()
}

func (ConsumerToken) String() string {
	return "xiangwan ConsumerToken{value:[REDACTED]}"
}

func (value ConsumerToken) GoString() string {
	return value.String()
}

func (LoginResult) String() string {
	return "xiangwan LoginResult{principal:[REDACTED],token:[REDACTED]}"
}

func (value LoginResult) GoString() string {
	return value.String()
}
