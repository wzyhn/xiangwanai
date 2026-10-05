package xiangwanapi

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights"
	datarightspostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights/postgres"
	"github.com/google/uuid"
)

func TestDataRightsServiceBindsOwnerPolicyAndOperation(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(247)
	principalID := apiUUID(248)
	operationKey := uuid.New()
	request := DataRightsSubmissionRequest{
		RequestType:  datarights.RequestTypeExport,
		RequestScope: datarights.RequestScopeAll,
		OperationKey: operationKey,
	}
	submission := datarights.Submission{
		TenantID:             tenantID,
		PrincipalID:          principalID,
		OperationKey:         operationKey,
		RequestType:          request.RequestType,
		RequestScope:         request.RequestScope,
		PrivacyPolicyVersion: "privacy-v3",
	}
	dataCase := dataRightsCaseForService(t, submission)
	writer := &fakeDataRightsWriter{result: datarightspostgres.SubmissionResult{
		Case: dataCase,
	}}
	reader := &fakeDataRightsReader{histories: []datarights.CaseHistory{}}
	service, err := NewDataRightsService(
		tenantID,
		"privacy-v3",
		writer,
		reader,
	)
	if err != nil {
		t.Fatalf("NewDataRightsService() error = %v", err)
	}
	result, err := service.Submit(context.Background(), principalID, request)
	if err != nil || result.Case.ID != dataCase.ID || writer.calls != 1 ||
		!reflect.DeepEqual(writer.submission, submission) {
		t.Fatalf("Submit() = %+v, %v, writer=%+v", result, err, writer)
	}
	histories, err := service.ListMine(context.Background(), principalID)
	if err != nil || histories == nil || reader.calls != 1 ||
		reader.tenantID != tenantID || reader.principalID != principalID {
		t.Fatalf("ListMine() = %+v, %v, reader=%+v", histories, err, reader)
	}
}

func TestDataRightsServiceFailsClosedWithoutPrivacyPolicy(t *testing.T) {
	t.Parallel()

	writer := &fakeDataRightsWriter{}
	service, err := NewDataRightsService(
		apiUUID(249),
		"",
		writer,
		&fakeDataRightsReader{histories: []datarights.CaseHistory{}},
	)
	if err != nil {
		t.Fatalf("NewDataRightsService() error = %v", err)
	}
	if _, err := service.Submit(
		context.Background(),
		apiUUID(250),
		DataRightsSubmissionRequest{
			RequestType:  datarights.RequestTypeAccess,
			RequestScope: datarights.RequestScopeProfile,
			OperationKey: uuid.New(),
		},
	); !errors.Is(err, ErrDataRightsPolicyUnavailable) || writer.calls != 0 {
		t.Fatalf("Submit(no policy) error=%v writer=%+v", err, writer)
	}
}

func TestDataRightsServiceValidatesConfigurationRequestAndProjection(t *testing.T) {
	t.Parallel()

	if _, err := NewDataRightsService(uuid.Nil, " invalid", nil, nil); !errors.Is(
		err,
		ErrInvalidDataRightsService,
	) {
		t.Fatalf("NewDataRightsService(invalid) error = %v", err)
	}
	writer := &fakeDataRightsWriter{}
	service, err := NewDataRightsService(
		apiUUID(251),
		"privacy-v3",
		writer,
		&fakeDataRightsReader{histories: []datarights.CaseHistory{}},
	)
	if err != nil {
		t.Fatalf("NewDataRightsService() error = %v", err)
	}
	if _, err := service.Submit(
		context.Background(),
		apiUUID(252),
		DataRightsSubmissionRequest{OperationKey: uuid.New()},
	); !errors.Is(err, ErrInvalidDataRightsRequest) || writer.calls != 0 {
		t.Fatalf("Submit(invalid) error=%v writer=%+v", err, writer)
	}

	foreignSubmission := datarights.Submission{
		TenantID:             apiUUID(253),
		PrincipalID:          apiUUID(254),
		OperationKey:         uuid.New(),
		RequestType:          datarights.RequestTypeAccess,
		RequestScope:         datarights.RequestScopeProfile,
		PrivacyPolicyVersion: "privacy-v3",
	}
	foreignWriter := &fakeDataRightsWriter{result: datarightspostgres.SubmissionResult{
		Case: dataRightsCaseForService(t, foreignSubmission),
	}}
	foreignService, err := NewDataRightsService(
		apiUUID(255),
		"privacy-v3",
		foreignWriter,
		&fakeDataRightsReader{histories: []datarights.CaseHistory{}},
	)
	if err != nil {
		t.Fatalf("NewDataRightsService(foreign) error = %v", err)
	}
	if _, err := foreignService.Submit(
		context.Background(),
		apiUUID(1),
		DataRightsSubmissionRequest{
			RequestType:  foreignSubmission.RequestType,
			RequestScope: foreignSubmission.RequestScope,
			OperationKey: foreignSubmission.OperationKey,
		},
	); !errors.Is(err, ErrInvalidDataRightsProjection) {
		t.Fatalf("Submit(foreign projection) error = %v", err)
	}
}

func TestDataRightsServiceRejectsIncompleteOrForeignHistory(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(2)
	principalID := apiUUID(3)
	submission := datarights.Submission{
		TenantID:             tenantID,
		PrincipalID:          principalID,
		OperationKey:         uuid.New(),
		RequestType:          datarights.RequestTypeAccess,
		RequestScope:         datarights.RequestScopeProfile,
		PrivacyPolicyVersion: "privacy-v3",
	}
	dataCase := dataRightsCaseForService(t, submission)
	actorID := principalID
	validEvent := datarights.CaseEvent{
		ID:                 uuid.New(),
		TenantID:           tenantID,
		CaseID:             dataCase.ID,
		CaseVersion:        1,
		EventType:          datarights.EventTypeSubmitted,
		ResultingStatus:    datarights.CaseStatusSubmitted,
		ActorPrincipalID:   &actorID,
		PolicyBasisVersion: submission.PrivacyPolicyVersion,
		OccurredAt:         dataCase.SubmittedAt,
	}
	tests := []struct {
		name    string
		history datarights.CaseHistory
	}{
		{
			name:    "missing event",
			history: datarights.CaseHistory{Case: dataCase, Events: []datarights.CaseEvent{}},
		},
		{
			name: "foreign event",
			history: datarights.CaseHistory{
				Case: dataCase,
				Events: []datarights.CaseEvent{
					func() datarights.CaseEvent {
						value := validEvent
						value.CaseID = uuid.New()
						return value
					}(),
				},
			},
		},
		{
			name: "wrong policy basis",
			history: datarights.CaseHistory{
				Case: dataCase,
				Events: []datarights.CaseEvent{
					func() datarights.CaseEvent {
						value := validEvent
						value.PolicyBasisVersion = "privacy-v2"
						return value
					}(),
				},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, err := NewDataRightsService(
				tenantID,
				"privacy-v3",
				&fakeDataRightsWriter{},
				&fakeDataRightsReader{histories: []datarights.CaseHistory{test.history}},
			)
			if err != nil {
				t.Fatalf("NewDataRightsService() error = %v", err)
			}
			if _, err := service.ListMine(
				context.Background(),
				principalID,
			); !errors.Is(err, ErrInvalidDataRightsProjection) {
				t.Fatalf("ListMine(invalid history) error = %v", err)
			}
		})
	}
}

func dataRightsCaseForService(
	t *testing.T,
	submission datarights.Submission,
) datarights.Case {
	t.Helper()
	fingerprint, err := datarights.SubmissionFingerprint(submission)
	if err != nil {
		t.Fatalf("SubmissionFingerprint() error = %v", err)
	}
	now := time.Now().UTC()
	return datarights.Case{
		ID:                   uuid.New(),
		TenantID:             submission.TenantID,
		PrincipalID:          submission.PrincipalID,
		OperationKey:         submission.OperationKey,
		RequestFingerprint:   fingerprint,
		RequestType:          submission.RequestType,
		RequestScope:         submission.RequestScope,
		PrivacyPolicyVersion: submission.PrivacyPolicyVersion,
		Status:               datarights.CaseStatusSubmitted,
		Version:              1,
		SubmittedAt:          now,
		UpdatedAt:            now,
	}
}

type fakeDataRightsWriter struct {
	result datarightspostgres.SubmissionResult
	err    error
	calls  int

	submission datarights.Submission
}

func (writer *fakeDataRightsWriter) Submit(
	_ context.Context,
	submission datarights.Submission,
) (datarightspostgres.SubmissionResult, error) {
	writer.calls++
	writer.submission = submission
	return writer.result, writer.err
}

type fakeDataRightsReader struct {
	histories   []datarights.CaseHistory
	err         error
	calls       int
	tenantID    uuid.UUID
	principalID uuid.UUID
}

func (reader *fakeDataRightsReader) ListMine(
	_ context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) ([]datarights.CaseHistory, error) {
	reader.calls++
	reader.tenantID = tenantID
	reader.principalID = principalID
	return reader.histories, reader.err
}
