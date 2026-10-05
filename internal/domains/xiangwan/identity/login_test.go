package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLoginServiceExchangesResolvesAndIssuesBoundToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 27, 1, 2, 3, 456789000, time.UTC)
	providerIdentity := ProviderIdentity{OpenID: "openid-1", UnionID: "unionid-1"}
	principalID := uuid.New()
	exchanger := &fakeCodeExchanger{identity: providerIdentity}
	resolver := &fakePrincipalResolver{principal: ResolvedPrincipal{
		ID:    principalID,
		IsNew: true,
	}}
	issuer := &fakeConsumerTokenIssuer{token: ConsumerToken{
		Value:     "header.payload.signature",
		ExpiresAt: now.Add(time.Hour),
	}}
	service, err := NewLoginService(exchanger, resolver, issuer, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewLoginService() error = %v", err)
	}
	result, err := service.Login(context.Background(), "single-use-code")
	if err != nil || result.PrincipalID != principalID || !result.IsNew ||
		result.Token != issuer.token || exchanger.code != "single-use-code" ||
		resolver.identity != providerIdentity || !resolver.occurredAt.Equal(now) ||
		issuer.principalID != principalID {
		t.Fatalf("Login() = %+v, %v; exchanger=%+v resolver=%+v issuer=%+v", result, err, exchanger, resolver, issuer)
	}
	formatted := fmt.Sprintf("%+v %+v %+v", providerIdentity, issuer.token, result)
	if strings.Contains(formatted, providerIdentity.OpenID) ||
		strings.Contains(formatted, issuer.token.Value) ||
		strings.Contains(formatted, principalID.String()) {
		t.Fatalf("login formatting leaked protected facts: %s", formatted)
	}
}

func TestLoginServiceRejectsInvalidInputsAndProjections(t *testing.T) {
	t.Parallel()

	if _, err := NewLoginService(nil, nil, nil, nil); !errors.Is(err, ErrInvalidLoginService) {
		t.Fatalf("NewLoginService(invalid) error = %v", err)
	}
	for _, code := range []string{"", " padded", "two words", "line\nbreak", strings.Repeat("x", MaxWeChatLoginCodeBytes+1)} {
		service, err := NewLoginService(
			&fakeCodeExchanger{},
			&fakePrincipalResolver{},
			&fakeConsumerTokenIssuer{},
			time.Now,
		)
		if err != nil {
			t.Fatalf("NewLoginService() error = %v", err)
		}
		if _, err := service.Login(context.Background(), code); !errors.Is(err, ErrInvalidLoginRequest) {
			t.Fatalf("Login(%q) error = %v", code, err)
		}
	}

	service, err := NewLoginService(
		&fakeCodeExchanger{identity: ProviderIdentity{}},
		&fakePrincipalResolver{},
		&fakeConsumerTokenIssuer{},
		time.Now,
	)
	if err != nil {
		t.Fatalf("NewLoginService() error = %v", err)
	}
	if _, err := service.Login(context.Background(), "valid-code"); !errors.Is(err, ErrInvalidLoginResult) {
		t.Fatalf("Login(invalid provider projection) error = %v", err)
	}
}

func TestLoginServicePreservesSafeDependencyClassifications(t *testing.T) {
	t.Parallel()

	wantErr := ErrWeChatCodeRejected
	service, err := NewLoginService(
		&fakeCodeExchanger{err: wantErr},
		&fakePrincipalResolver{},
		&fakeConsumerTokenIssuer{},
		time.Now,
	)
	if err != nil {
		t.Fatalf("NewLoginService() error = %v", err)
	}
	if _, err := service.Login(context.Background(), "valid-code"); !errors.Is(err, wantErr) {
		t.Fatalf("Login(provider failure) error = %v", err)
	}
}

type fakeCodeExchanger struct {
	identity ProviderIdentity
	err      error
	code     string
}

func (fake *fakeCodeExchanger) Exchange(
	_ context.Context,
	code string,
) (ProviderIdentity, error) {
	fake.code = code
	return fake.identity, fake.err
}

type fakePrincipalResolver struct {
	principal  ResolvedPrincipal
	err        error
	identity   ProviderIdentity
	occurredAt time.Time
}

func (fake *fakePrincipalResolver) Resolve(
	_ context.Context,
	identity ProviderIdentity,
	occurredAt time.Time,
) (ResolvedPrincipal, error) {
	fake.identity = identity
	fake.occurredAt = occurredAt
	return fake.principal, fake.err
}

type fakeConsumerTokenIssuer struct {
	token       ConsumerToken
	err         error
	principalID uuid.UUID
}

func (fake *fakeConsumerTokenIssuer) IssueConsumerToken(
	principalID uuid.UUID,
) (ConsumerToken, error) {
	fake.principalID = principalID
	return fake.token, fake.err
}
