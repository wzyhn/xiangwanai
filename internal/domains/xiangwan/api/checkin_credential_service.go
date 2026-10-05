package xiangwanapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	checkinpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin/postgres"
	"github.com/google/uuid"
)

var (
	ErrInvalidCheckinCredentialService = errors.New(
		"invalid xiangwan Checkin credential service",
	)
	ErrInvalidCheckinCredentialRequest = errors.New(
		"invalid xiangwan Checkin credential request",
	)
)

type checkinCredentialIssuer interface {
	Issue(
		context.Context,
		checkinpostgres.IssueRegistrationCredentialCommand,
	) (checkin.IssuedCredential, error)
}

type CheckinCredentialService struct {
	tenantID uuid.UUID
	ttl      time.Duration
	issuer   checkinCredentialIssuer
}

func NewCheckinCredentialService(
	tenantID uuid.UUID,
	ttl time.Duration,
	issuer checkinCredentialIssuer,
) (*CheckinCredentialService, error) {
	if tenantID == uuid.Nil || ttl <= 0 || ttl > checkin.MaxCredentialTTL ||
		issuer == nil {
		return nil, ErrInvalidCheckinCredentialService
	}
	return &CheckinCredentialService{
		tenantID: tenantID,
		ttl:      ttl,
		issuer:   issuer,
	}, nil
}

func (service *CheckinCredentialService) Issue(
	ctx context.Context,
	principalID uuid.UUID,
	registrationID uuid.UUID,
) (checkin.IssuedCredential, error) {
	if service == nil || service.tenantID == uuid.Nil || service.ttl <= 0 ||
		service.ttl > checkin.MaxCredentialTTL || service.issuer == nil {
		return checkin.IssuedCredential{}, ErrInvalidCheckinCredentialService
	}
	if ctx == nil || principalID == uuid.Nil || registrationID == uuid.Nil {
		return checkin.IssuedCredential{}, ErrInvalidCheckinCredentialRequest
	}
	issued, err := service.issuer.Issue(
		ctx,
		checkinpostgres.IssueRegistrationCredentialCommand{
			TenantID:       service.tenantID,
			RegistrationID: registrationID,
			PrincipalID:    principalID,
			TTL:            service.ttl,
		},
	)
	if err != nil {
		return checkin.IssuedCredential{}, fmt.Errorf(
			"issue authenticated xiangwan Checkin credential: %w",
			err,
		)
	}
	if checkin.ValidateCredential(issued.Credential) != nil ||
		issued.Credential.TenantID != service.tenantID ||
		issued.Credential.PrincipalID != principalID ||
		issued.Credential.RegistrationID != registrationID ||
		issued.QRToken == "" || issued.BackupCode == "" {
		return checkin.IssuedCredential{},
			checkinpostgres.ErrRegistrationCredentialTransactionConflict
	}
	return issued, nil
}
