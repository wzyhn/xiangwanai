package xiangwanapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	checkinpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin/postgres"
	"github.com/google/uuid"
)

func TestOnsiteCheckinServiceBindsOperatorAndExactSession(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(1)
	operatorID := apiUUID(2)
	operator := testAdminPrincipal(operatorID)
	request := knownOnsiteVerificationRequest()
	attempt := knownOnsiteVerificationAttempt(
		request,
		tenantID,
		operatorID,
		checkin.VerificationDecisionValid,
	)
	verifier := &fakeOnsiteVerifier{
		result: checkinpostgres.VerifyCredentialResult{Attempt: attempt},
	}
	recorder := &fakeOnsiteRecorder{}
	service, err := NewOnsiteCheckinService(tenantID, verifier, recorder)
	if err != nil {
		t.Fatalf("NewOnsiteCheckinService() error = %v", err)
	}
	result, err := service.Verify(context.Background(), operator, request)
	if err != nil || result.Attempt.ID != attempt.ID || verifier.calls != 1 {
		t.Fatalf("Verify() = %+v, calls=%d, error=%v", result, verifier.calls, err)
	}
	want := checkinpostgres.VerifyCredentialCommand{
		TenantID:       tenantID,
		SeriesID:       request.SeriesID,
		InstanceID:     request.InstanceID,
		SessionID:      request.SessionID,
		ActorID:        operatorID,
		IdentityLinkID: operator.IdentityLinkID,
		PresentedKind:  request.PresentedKind,
		PresentedValue: request.PresentedValue,
		IdempotencyKey: request.IdempotencyKey.String(),
	}
	if verifier.command != want {
		t.Fatalf("Verify command = %+v, want %+v", verifier.command, want)
	}
}

func TestOnsiteCheckinServiceRecordsOnlyCoherentResult(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(10)
	operatorID := apiUUID(11)
	operator := testAdminPrincipal(operatorID)
	request := knownOnsiteRecordRequest()
	value, err := checkin.New(checkin.NewCommand{
		TenantID:       tenantID,
		RegistrationID: request.RegistrationID,
		SeriesID:       request.SeriesID,
		InstanceID:     request.InstanceID,
		SessionID:      request.SessionID,
		PrincipalID:    apiUUID(12),
		CheckedInBy:    operatorID,
		At:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("checkin.New() error = %v", err)
	}
	event, err := checkin.NewEvent(
		nil,
		value,
		onsiteCheckinRecordEventKey(request),
	)
	if err != nil {
		t.Fatalf("checkin.NewEvent() error = %v", err)
	}
	recorder := &fakeOnsiteRecorder{
		result: checkinpostgres.RecordCheckinResult{
			Checkin: value,
			Event:   &event,
		},
	}
	service, err := NewOnsiteCheckinService(
		tenantID,
		&fakeOnsiteVerifier{},
		recorder,
	)
	if err != nil {
		t.Fatalf("NewOnsiteCheckinService() error = %v", err)
	}
	result, err := service.Record(context.Background(), operator, request)
	if err != nil || result.Checkin.ID != value.ID || recorder.calls != 1 {
		t.Fatalf("Record() = %+v, calls=%d, error=%v", result, recorder.calls, err)
	}
	want := checkinpostgres.RecordCheckinCommand{
		TenantID:              tenantID,
		SeriesID:              request.SeriesID,
		InstanceID:            request.InstanceID,
		SessionID:             request.SessionID,
		RegistrationID:        request.RegistrationID,
		CredentialID:          request.CredentialID,
		VerificationAttemptID: request.VerificationAttemptID,
		ActorID:               operatorID,
		IdentityLinkID:        operator.IdentityLinkID,
		IdempotencyKey:        onsiteCheckinRecordEventKey(request),
	}
	if recorder.command != want {
		t.Fatalf("Record command = %+v, want %+v", recorder.command, want)
	}
	recorder.result = checkinpostgres.RecordCheckinResult{
		Checkin:   value,
		Duplicate: true,
	}
	replayed, err := service.Record(context.Background(), operator, request)
	if err != nil || !replayed.Duplicate || replayed.Event != nil {
		t.Fatalf("Record(existing Checkin) = %+v, error=%v", replayed, err)
	}

	otherOperatorID := apiUUID(13)
	replayed, err = service.Record(
		context.Background(),
		testAdminPrincipal(otherOperatorID),
		request,
	)
	if err != nil || !replayed.Duplicate ||
		replayed.Checkin.CheckedInBy != operatorID {
		t.Fatalf(
			"Record(existing Checkin by another operator) = %+v, error=%v",
			replayed,
			err,
		)
	}

	revoked, err := checkin.Revoke(value, checkin.RevokeCommand{
		RevokedBy: apiUUID(14),
		Reason:    "attendance corrected",
		At:        value.CheckedInAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("checkin.Revoke() error = %v", err)
	}
	recorder.result = checkinpostgres.RecordCheckinResult{
		Checkin:   revoked,
		Event:     &event,
		Duplicate: true,
	}
	replayed, err = service.Record(context.Background(), operator, request)
	if err != nil || !replayed.Duplicate ||
		replayed.Checkin.CheckinStatus != checkin.StatusRevoked {
		t.Fatalf("Record(revoked Checkin) = %+v, error=%v", replayed, err)
	}
}

func TestOnsiteCheckinServiceRejectsInvalidRequestsBeforePostgres(t *testing.T) {
	t.Parallel()

	verifier := &fakeOnsiteVerifier{}
	recorder := &fakeOnsiteRecorder{}
	service, err := NewOnsiteCheckinService(apiUUID(20), verifier, recorder)
	if err != nil {
		t.Fatalf("NewOnsiteCheckinService() error = %v", err)
	}
	verification := knownOnsiteVerificationRequest()
	verification.IdempotencyKey = apiUUID(21)
	if _, err := service.Verify(
		context.Background(),
		testAdminPrincipal(apiUUID(22)),
		verification,
	); !errors.Is(err, ErrInvalidOnsiteCheckinRequest) {
		t.Fatalf("Verify(invalid) error = %v", err)
	}
	record := knownOnsiteRecordRequest()
	record.CredentialID = uuid.Nil
	if _, err := service.Record(
		context.Background(),
		testAdminPrincipal(apiUUID(23)),
		record,
	); !errors.Is(err, ErrInvalidOnsiteCheckinRequest) {
		t.Fatalf("Record(invalid) error = %v", err)
	}
	if verifier.calls != 0 || recorder.calls != 0 {
		t.Fatalf("invalid requests reached delegates: verify=%d record=%d", verifier.calls, recorder.calls)
	}
}

func TestOnsiteCheckinServiceKeepsNewCheckinResultStrict(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(15)
	operatorID := apiUUID(16)
	operator := testAdminPrincipal(operatorID)
	request := knownOnsiteRecordRequest()
	value, err := checkin.New(checkin.NewCommand{
		TenantID:       tenantID,
		RegistrationID: request.RegistrationID,
		SeriesID:       request.SeriesID,
		InstanceID:     request.InstanceID,
		SessionID:      request.SessionID,
		PrincipalID:    apiUUID(17),
		CheckedInBy:    apiUUID(18),
		At:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("checkin.New() error = %v", err)
	}
	recorder := &fakeOnsiteRecorder{
		result: checkinpostgres.RecordCheckinResult{Checkin: value},
	}
	service, err := NewOnsiteCheckinService(
		tenantID,
		&fakeOnsiteVerifier{},
		recorder,
	)
	if err != nil {
		t.Fatalf("NewOnsiteCheckinService() error = %v", err)
	}
	if _, err := service.Record(
		context.Background(),
		operator,
		request,
	); !errors.Is(err, ErrOnsiteCheckinResponseConflict) {
		t.Fatalf("Record(new foreign-operator fact) error = %v", err)
	}

	ownValue, err := checkin.New(checkin.NewCommand{
		TenantID:       tenantID,
		RegistrationID: request.RegistrationID,
		SeriesID:       request.SeriesID,
		InstanceID:     request.InstanceID,
		SessionID:      request.SessionID,
		PrincipalID:    apiUUID(17),
		CheckedInBy:    operatorID,
		At:             value.CheckedInAt,
	})
	if err != nil {
		t.Fatalf("checkin.New(own) error = %v", err)
	}
	revoked, err := checkin.Revoke(ownValue, checkin.RevokeCommand{
		RevokedBy: operatorID,
		Reason:    "attendance corrected",
		At:        ownValue.CheckedInAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("checkin.Revoke() error = %v", err)
	}
	recorder.result.Checkin = revoked
	if _, err := service.Record(
		context.Background(),
		operator,
		request,
	); !errors.Is(err, ErrOnsiteCheckinResponseConflict) {
		t.Fatalf("Record(new revoked fact) error = %v", err)
	}
}

func TestOnsiteCheckinServiceRejectsCrossScopeDelegateResults(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(30)
	operatorID := apiUUID(31)
	operator := testAdminPrincipal(operatorID)
	verification := knownOnsiteVerificationRequest()
	attempt := knownOnsiteVerificationAttempt(
		verification,
		tenantID,
		operatorID,
		checkin.VerificationDecisionValid,
	)
	attempt.RequestedSessionID = apiUUID(32)
	service, err := NewOnsiteCheckinService(
		tenantID,
		&fakeOnsiteVerifier{result: checkinpostgres.VerifyCredentialResult{Attempt: attempt}},
		&fakeOnsiteRecorder{},
	)
	if err != nil {
		t.Fatalf("NewOnsiteCheckinService() error = %v", err)
	}
	if _, err := service.Verify(
		context.Background(),
		operator,
		verification,
	); !errors.Is(err, ErrOnsiteCheckinResponseConflict) {
		t.Fatalf("Verify(cross scope) error = %v", err)
	}

	record := knownOnsiteRecordRequest()
	value, err := checkin.New(checkin.NewCommand{
		TenantID:       tenantID,
		RegistrationID: record.RegistrationID,
		SeriesID:       record.SeriesID,
		InstanceID:     record.InstanceID,
		SessionID:      apiUUID(33),
		PrincipalID:    apiUUID(34),
		CheckedInBy:    operatorID,
		At:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("checkin.New() error = %v", err)
	}
	service.recorder = &fakeOnsiteRecorder{
		result: checkinpostgres.RecordCheckinResult{Checkin: value},
	}
	if _, err := service.Record(
		context.Background(),
		operator,
		record,
	); !errors.Is(err, ErrOnsiteCheckinResponseConflict) {
		t.Fatalf("Record(cross scope) error = %v", err)
	}
}

func TestOnsiteCheckinServicePreservesPostgresFailures(t *testing.T) {
	t.Parallel()

	verifyFailure := errors.New("verify database unavailable")
	recordFailure := errors.New("record database unavailable")
	service, err := NewOnsiteCheckinService(
		apiUUID(40),
		&fakeOnsiteVerifier{err: verifyFailure},
		&fakeOnsiteRecorder{err: recordFailure},
	)
	if err != nil {
		t.Fatalf("NewOnsiteCheckinService() error = %v", err)
	}
	if _, err := service.Verify(
		context.Background(),
		testAdminPrincipal(apiUUID(41)),
		knownOnsiteVerificationRequest(),
	); !errors.Is(err, verifyFailure) {
		t.Fatalf("Verify() error = %v", err)
	}
	if _, err := service.Record(
		context.Background(),
		testAdminPrincipal(apiUUID(41)),
		knownOnsiteRecordRequest(),
	); !errors.Is(err, recordFailure) {
		t.Fatalf("Record() error = %v", err)
	}
}

func knownOnsiteVerificationRequest() OnsiteCheckinVerificationRequest {
	return OnsiteCheckinVerificationRequest{
		SeriesID:       apiUUID(50),
		InstanceID:     apiUUID(51),
		SessionID:      apiUUID(52),
		PresentedKind:  checkin.PresentedCredentialKindQRToken,
		PresentedValue: "xw-checkin-v1.synthetic-token",
		IdempotencyKey: uuid.New(),
	}
}

func knownOnsiteRecordRequest() OnsiteCheckinRecordRequest {
	return OnsiteCheckinRecordRequest{
		SeriesID:              apiUUID(60),
		InstanceID:            apiUUID(61),
		SessionID:             apiUUID(62),
		RegistrationID:        apiUUID(63),
		CredentialID:          apiUUID(64),
		VerificationAttemptID: apiUUID(65),
	}
}

func knownOnsiteVerificationAttempt(
	request OnsiteCheckinVerificationRequest,
	tenantID uuid.UUID,
	operatorID uuid.UUID,
	decision checkin.VerificationDecision,
) checkin.VerificationAttempt {
	at := time.Now().UTC()
	value := checkin.VerificationAttempt{
		ID:                  apiUUID(70),
		TenantID:            tenantID,
		RequestedSeriesID:   request.SeriesID,
		RequestedInstanceID: request.InstanceID,
		RequestedSessionID:  request.SessionID,
		ActorID:             operatorID,
		PresentedKind:       request.PresentedKind,
		Decision:            decision,
		IdempotencyKey:      request.IdempotencyKey.String(),
		RequestFingerprint:  checkin.CredentialDigest{1},
		OccurredAt:          at,
		CreatedAt:           at,
	}
	if decision != checkin.VerificationDecisionInvalidCredential {
		credentialID := apiUUID(71)
		credentialJTI := apiUUID(72)
		registrationID := apiUUID(73)
		principalID := apiUUID(74)
		value.CredentialID = &credentialID
		value.CredentialJTI = &credentialJTI
		value.RegistrationID = &registrationID
		value.PrincipalID = &principalID
	}
	if decision == checkin.VerificationDecisionAlreadyCheckedIn {
		checkinID := apiUUID(75)
		value.CheckinID = &checkinID
	}
	return value
}

type fakeOnsiteVerifier struct {
	result checkinpostgres.VerifyCredentialResult
	err    error

	calls   int
	command checkinpostgres.VerifyCredentialCommand
}

func (fake *fakeOnsiteVerifier) Verify(
	_ context.Context,
	command checkinpostgres.VerifyCredentialCommand,
) (checkinpostgres.VerifyCredentialResult, error) {
	fake.calls++
	fake.command = command
	return fake.result, fake.err
}

type fakeOnsiteRecorder struct {
	result checkinpostgres.RecordCheckinResult
	err    error

	calls   int
	command checkinpostgres.RecordCheckinCommand
}

func (fake *fakeOnsiteRecorder) Record(
	_ context.Context,
	command checkinpostgres.RecordCheckinCommand,
) (checkinpostgres.RecordCheckinResult, error) {
	fake.calls++
	fake.command = command
	return fake.result, fake.err
}
