package xiangwanapi

import (
	"context"
	"errors"
	"fmt"

	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/google/uuid"
)

var (
	ErrInvalidHostApplicationSubmissionService = errors.New(
		"invalid xiangwan Host Application submission service",
	)
	ErrInvalidHostApplicationSubmissionRequest = errors.New(
		"invalid xiangwan Host Application submission request",
	)
)

type hostApplicationSubmitter interface {
	Apply(
		context.Context,
		peoplepostgres.ApplyHostApplicationCommand,
	) (peoplepostgres.HostApplicationResult, error)
}

type HostApplicationSubmissionRequest struct {
	PersonalIntroduction         string
	RelevantExperience           string
	Availability                 string
	ContactMethod                string
	ExpectedCycle                string
	ExpectedPrivacyPolicyVersion string
	ExpectedPolicyVersion        string
	Consent                      bool
}

type HostApplicationSubmissionService struct {
	tenantID uuid.UUID
	writer   hostApplicationSubmitter
}

func NewHostApplicationSubmissionService(
	tenantID uuid.UUID,
	writer hostApplicationSubmitter,
) (*HostApplicationSubmissionService, error) {
	if tenantID == uuid.Nil || writer == nil {
		return nil, ErrInvalidHostApplicationSubmissionService
	}
	return &HostApplicationSubmissionService{
		tenantID: tenantID,
		writer:   writer,
	}, nil
}

func (service *HostApplicationSubmissionService) Apply(
	ctx context.Context,
	principalID uuid.UUID,
	request HostApplicationSubmissionRequest,
) (peoplepostgres.HostApplicationResult, error) {
	if service == nil || service.tenantID == uuid.Nil || service.writer == nil {
		return peoplepostgres.HostApplicationResult{},
			ErrInvalidHostApplicationSubmissionService
	}
	if ctx == nil || principalID == uuid.Nil {
		return peoplepostgres.HostApplicationResult{},
			ErrInvalidHostApplicationSubmissionRequest
	}
	result, err := service.writer.Apply(
		ctx,
		peoplepostgres.ApplyHostApplicationCommand{
			TenantID:             service.tenantID,
			PrincipalID:          principalID,
			PersonalIntroduction: request.PersonalIntroduction,
			RelevantExperience:   request.RelevantExperience,
			Availability:         request.Availability,
			ContactMethod:        request.ContactMethod,
			ExpectedCycle:        request.ExpectedCycle, ExpectedPolicyVersion: request.ExpectedPolicyVersion, ExpectedPrivacyPolicyVersion: request.ExpectedPrivacyPolicyVersion, Consent: request.Consent,
		},
	)
	if err != nil {
		return peoplepostgres.HostApplicationResult{}, fmt.Errorf(
			"apply for xiangwan Host role: %w",
			err,
		)
	}
	return result, nil
}

func (service *HostApplicationSubmissionService) Withdraw(ctx context.Context, principal, application uuid.UUID, version int64) (peoplepostgres.HostApplicationResult, error) {
	writer, ok := service.writer.(interface {
		Withdraw(context.Context, peoplepostgres.WithdrawHostApplicationCommand) (peoplepostgres.HostApplicationResult, error)
	})
	if !ok || ctx == nil || principal == uuid.Nil || application == uuid.Nil || version < 1 {
		return peoplepostgres.HostApplicationResult{}, ErrInvalidHostApplicationSubmissionRequest
	}
	return writer.Withdraw(ctx, peoplepostgres.WithdrawHostApplicationCommand{TenantID: service.tenantID, PrincipalID: principal, ApplicationID: application, ExpectedVersion: version})
}
