package xiangwanapi

import (
	"context"
	"errors"
	"fmt"

	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/google/uuid"
)

var (
	ErrInvalidRegistrationCancellationService = errors.New(
		"invalid xiangwan Registration cancellation service",
	)
	ErrInvalidRegistrationCancellationRequest = errors.New(
		"invalid xiangwan Registration cancellation request",
	)
)

type registrationCancellationWriter interface {
	Cancel(
		context.Context,
		registrationpostgres.CancelRegistrationCommand,
	) (registrationpostgres.RegistrationCancellationResult, error)
}

// RegistrationCancellationService binds the single-customer runtime tenant and
// authenticated principal to the PostgreSQL cancellation transaction.
type RegistrationCancellationService struct {
	tenantID  uuid.UUID
	canceller registrationCancellationWriter
}

func NewRegistrationCancellationService(
	tenantID uuid.UUID,
	canceller registrationCancellationWriter,
) (*RegistrationCancellationService, error) {
	if tenantID == uuid.Nil || canceller == nil {
		return nil, ErrInvalidRegistrationCancellationService
	}
	return &RegistrationCancellationService{
		tenantID:  tenantID,
		canceller: canceller,
	}, nil
}

func (service *RegistrationCancellationService) Cancel(
	ctx context.Context,
	principalID uuid.UUID,
	registrationID uuid.UUID,
) (registrationpostgres.RegistrationCancellationResult, error) {
	if service == nil || service.tenantID == uuid.Nil || service.canceller == nil {
		return registrationpostgres.RegistrationCancellationResult{},
			ErrInvalidRegistrationCancellationService
	}
	if ctx == nil || principalID == uuid.Nil || registrationID == uuid.Nil {
		return registrationpostgres.RegistrationCancellationResult{},
			ErrInvalidRegistrationCancellationRequest
	}
	result, err := service.canceller.Cancel(
		ctx,
		registrationpostgres.CancelRegistrationCommand{
			TenantID:       service.tenantID,
			RegistrationID: registrationID,
			ActorID:        principalID,
			Source:         registrationpostgres.CancellationSourceSelf,
		},
	)
	if err != nil {
		return registrationpostgres.RegistrationCancellationResult{}, fmt.Errorf(
			"cancel authenticated xiangwan Registration: %w",
			err,
		)
	}
	return result, nil
}
