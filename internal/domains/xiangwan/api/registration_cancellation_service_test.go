package xiangwanapi

import (
	"context"
	"errors"
	"testing"

	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/google/uuid"
)

func TestRegistrationCancellationServiceBindsTenantPrincipalAndSelfSource(
	t *testing.T,
) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	registrationID := uuid.New()
	writer := &fakeRegistrationCancellationWriter{
		result: registrationpostgres.RegistrationCancellationResult{},
	}
	service, err := NewRegistrationCancellationService(tenantID, writer)
	if err != nil {
		t.Fatalf("NewRegistrationCancellationService() error = %v", err)
	}
	if _, err := service.Cancel(
		context.Background(),
		principalID,
		registrationID,
	); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if writer.calls != 1 || writer.command.TenantID != tenantID ||
		writer.command.RegistrationID != registrationID ||
		writer.command.ActorID != principalID ||
		writer.command.Source != registrationpostgres.CancellationSourceSelf ||
		writer.command.Reason != "" {
		t.Fatalf("Cancel() command = %+v", writer.command)
	}
}

func TestRegistrationCancellationServiceValidatesBoundaryAndPreservesCause(
	t *testing.T,
) {
	t.Parallel()

	if _, err := NewRegistrationCancellationService(uuid.Nil, nil); !errors.Is(
		err,
		ErrInvalidRegistrationCancellationService,
	) {
		t.Fatalf("NewRegistrationCancellationService(invalid) error = %v", err)
	}
	wantErr := registrationpostgres.ErrRegistrationCancellationPolicyMissing
	writer := &fakeRegistrationCancellationWriter{err: wantErr}
	service, err := NewRegistrationCancellationService(uuid.New(), writer)
	if err != nil {
		t.Fatalf("NewRegistrationCancellationService() error = %v", err)
	}
	if _, err := service.Cancel(
		context.Background(),
		uuid.Nil,
		uuid.New(),
	); !errors.Is(
		err,
		ErrInvalidRegistrationCancellationRequest,
	) || writer.calls != 0 {
		t.Fatalf("Cancel(invalid) error = %v calls=%d", err, writer.calls)
	}
	if _, err := service.Cancel(
		context.Background(),
		uuid.New(),
		uuid.New(),
	); !errors.Is(err, wantErr) {
		t.Fatalf("Cancel(failure) error = %v", err)
	}
}

type fakeRegistrationCancellationWriter struct {
	result  registrationpostgres.RegistrationCancellationResult
	err     error
	calls   int
	command registrationpostgres.CancelRegistrationCommand
}

func (writer *fakeRegistrationCancellationWriter) Cancel(
	_ context.Context,
	command registrationpostgres.CancelRegistrationCommand,
) (registrationpostgres.RegistrationCancellationResult, error) {
	writer.calls++
	writer.command = command
	return writer.result, writer.err
}
