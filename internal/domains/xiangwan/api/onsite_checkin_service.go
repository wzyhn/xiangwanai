package xiangwanapi

import (
	"context"
	"errors"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	checkinpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin/postgres"
	"github.com/google/uuid"
)

var (
	ErrInvalidOnsiteCheckinService = errors.New(
		"invalid xiangwan on-site Checkin service",
	)
	ErrInvalidOnsiteCheckinRequest = errors.New(
		"invalid xiangwan on-site Checkin request",
	)
	ErrOnsiteCheckinResponseConflict = errors.New(
		"xiangwan on-site Checkin response conflict",
	)
)

type onsiteCredentialVerifier interface {
	Verify(
		context.Context,
		checkinpostgres.VerifyCredentialCommand,
	) (checkinpostgres.VerifyCredentialResult, error)
}

type onsiteCheckinRecorder interface {
	Record(
		context.Context,
		checkinpostgres.RecordCheckinCommand,
	) (checkinpostgres.RecordCheckinResult, error)
}

type OnsiteCheckinVerificationRequest struct {
	SeriesID       uuid.UUID
	InstanceID     uuid.UUID
	SessionID      uuid.UUID
	PresentedKind  checkin.PresentedCredentialKind
	PresentedValue string
	IdempotencyKey uuid.UUID
}

type OnsiteCheckinRecordRequest struct {
	SeriesID              uuid.UUID
	InstanceID            uuid.UUID
	SessionID             uuid.UUID
	RegistrationID        uuid.UUID
	CredentialID          uuid.UUID
	VerificationAttemptID uuid.UUID
}

// OnsiteCheckinService fixes the customer Tenant and authenticated operator
// before delegating to the PostgreSQL verifier and recorder. Both delegates
// re-authorize the operator inside their serializable transactions.
type OnsiteCheckinService struct {
	tenantID uuid.UUID
	verifier onsiteCredentialVerifier
	recorder onsiteCheckinRecorder
}

func NewOnsiteCheckinService(
	tenantID uuid.UUID,
	verifier onsiteCredentialVerifier,
	recorder onsiteCheckinRecorder,
) (*OnsiteCheckinService, error) {
	if tenantID == uuid.Nil || verifier == nil || recorder == nil {
		return nil, ErrInvalidOnsiteCheckinService
	}
	return &OnsiteCheckinService{
		tenantID: tenantID,
		verifier: verifier,
		recorder: recorder,
	}, nil
}

func (service *OnsiteCheckinService) Verify(
	ctx context.Context,
	operator xiangwanadmin.Principal,
	request OnsiteCheckinVerificationRequest,
) (checkinpostgres.VerifyCredentialResult, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.verifier == nil || service.recorder == nil {
		return checkinpostgres.VerifyCredentialResult{},
			ErrInvalidOnsiteCheckinService
	}
	if ctx == nil || operator.PrincipalID == uuid.Nil ||
		operator.IdentityLinkID == uuid.Nil ||
		!validOnsiteCheckinVerificationRequest(request) {
		return checkinpostgres.VerifyCredentialResult{},
			ErrInvalidOnsiteCheckinRequest
	}
	result, err := service.verifier.Verify(
		ctx,
		checkinpostgres.VerifyCredentialCommand{
			TenantID:       service.tenantID,
			SeriesID:       request.SeriesID,
			InstanceID:     request.InstanceID,
			SessionID:      request.SessionID,
			ActorID:        operator.PrincipalID,
			IdentityLinkID: operator.IdentityLinkID,
			PresentedKind:  request.PresentedKind,
			PresentedValue: request.PresentedValue,
			IdempotencyKey: request.IdempotencyKey.String(),
		},
	)
	if err != nil {
		return checkinpostgres.VerifyCredentialResult{}, err
	}
	attempt := result.Attempt
	if checkin.ValidateVerificationAttempt(attempt) != nil ||
		attempt.TenantID != service.tenantID ||
		attempt.RequestedSeriesID != request.SeriesID ||
		attempt.RequestedInstanceID != request.InstanceID ||
		attempt.RequestedSessionID != request.SessionID ||
		attempt.ActorID != operator.PrincipalID ||
		attempt.PresentedKind != request.PresentedKind ||
		attempt.IdempotencyKey != request.IdempotencyKey.String() {
		return checkinpostgres.VerifyCredentialResult{},
			ErrOnsiteCheckinResponseConflict
	}
	return result, nil
}

func (service *OnsiteCheckinService) Record(
	ctx context.Context,
	operator xiangwanadmin.Principal,
	request OnsiteCheckinRecordRequest,
) (checkinpostgres.RecordCheckinResult, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.verifier == nil || service.recorder == nil {
		return checkinpostgres.RecordCheckinResult{},
			ErrInvalidOnsiteCheckinService
	}
	if ctx == nil || operator.PrincipalID == uuid.Nil ||
		operator.IdentityLinkID == uuid.Nil ||
		!validOnsiteCheckinRecordRequest(request) {
		return checkinpostgres.RecordCheckinResult{},
			ErrInvalidOnsiteCheckinRequest
	}
	result, err := service.recorder.Record(
		ctx,
		checkinpostgres.RecordCheckinCommand{
			TenantID:              service.tenantID,
			SeriesID:              request.SeriesID,
			InstanceID:            request.InstanceID,
			SessionID:             request.SessionID,
			RegistrationID:        request.RegistrationID,
			CredentialID:          request.CredentialID,
			VerificationAttemptID: request.VerificationAttemptID,
			ActorID:               operator.PrincipalID,
			IdentityLinkID:        operator.IdentityLinkID,
			IdempotencyKey:        onsiteCheckinRecordEventKey(request),
		},
	)
	if err != nil {
		return checkinpostgres.RecordCheckinResult{}, err
	}
	value := result.Checkin
	if checkin.Validate(value) != nil ||
		value.TenantID != service.tenantID ||
		value.SeriesID != request.SeriesID ||
		value.InstanceID != request.InstanceID ||
		value.SessionID != request.SessionID ||
		value.RegistrationID != request.RegistrationID ||
		(!result.Duplicate &&
			(value.CheckedInBy != operator.PrincipalID ||
				value.CheckinStatus != checkin.StatusCheckedIn)) ||
		!validOnsiteCheckinEvent(
			result.Event,
			result.Duplicate,
			value,
			request,
		) {
		return checkinpostgres.RecordCheckinResult{},
			ErrOnsiteCheckinResponseConflict
	}
	return result, nil
}

func validOnsiteCheckinVerificationRequest(
	request OnsiteCheckinVerificationRequest,
) bool {
	return request.SeriesID != uuid.Nil &&
		request.InstanceID != uuid.Nil &&
		request.SessionID != uuid.Nil &&
		(request.PresentedKind == checkin.PresentedCredentialKindQRToken ||
			request.PresentedKind == checkin.PresentedCredentialKindBackupCode) &&
		request.PresentedValue != "" &&
		len(request.PresentedValue) <= checkin.MaxPresentedCredentialLength &&
		validOnsiteOperationKey(request.IdempotencyKey)
}

func validOnsiteCheckinRecordRequest(request OnsiteCheckinRecordRequest) bool {
	return request.SeriesID != uuid.Nil &&
		request.InstanceID != uuid.Nil &&
		request.SessionID != uuid.Nil &&
		request.RegistrationID != uuid.Nil &&
		request.CredentialID != uuid.Nil &&
		request.VerificationAttemptID != uuid.Nil
}

func validOnsiteOperationKey(value uuid.UUID) bool {
	return value != uuid.Nil && value.Version() == 4 &&
		value.Variant() == uuid.RFC4122
}

func validOnsiteCheckinEvent(
	event *checkin.Event,
	duplicate bool,
	value checkin.Checkin,
	request OnsiteCheckinRecordRequest,
) bool {
	if event == nil {
		return duplicate
	}
	return checkin.ValidateEvent(*event) == nil &&
		event.TenantID == value.TenantID &&
		event.CheckinID == value.ID &&
		event.RegistrationID == request.RegistrationID &&
		event.SessionID == request.SessionID &&
		event.EventType == checkin.EventTypeCheckedIn &&
		event.ActorID == value.CheckedInBy &&
		event.IdempotencyKey == onsiteCheckinRecordEventKey(request)
}

func onsiteCheckinRecordEventKey(request OnsiteCheckinRecordRequest) string {
	return "registration:checkin:" + request.RegistrationID.String()
}
