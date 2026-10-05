package xiangwanapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile"
	consumerprofilepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/contentsecurity"
	"github.com/google/uuid"
)

const consumerProfileTestAppID = "wx1234567890abcdef"

func TestConsumerProfileServiceReadsValidatedOwnerProjection(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(21)
	principalID := apiUUID(22)
	snapshot := consumerprofile.Snapshot{
		Principal: consumerprofile.PrincipalProfile{ETag: "pp_opaque"},
		Published: consumerprofile.PublishedProfile{
			Fields: consumerprofile.Fields{Tags: []string{}},
		},
	}
	reader := &fakeConsumerProfileReader{snapshot: snapshot}
	service, err := NewConsumerProfileService(
		tenantID,
		consumerProfileTestAppID,
		"",
		reader,
		&fakeConsumerProfileWriter{},
		&fakeConsumerProfileChecker{},
	)
	if err != nil {
		t.Fatalf("NewConsumerProfileService() error = %v", err)
	}
	got, err := service.GetMine(context.Background(), principalID)
	if err != nil || got.Principal.ETag != snapshot.Principal.ETag ||
		reader.readCalls != 1 || reader.tenantID != tenantID ||
		reader.principalID != principalID {
		t.Fatalf("GetMine()=%+v,%v reader=%+v", got, err, reader)
	}

	reader.snapshot.Principal.ETag = ""
	if _, err := service.GetMine(
		context.Background(),
		principalID,
	); !errors.Is(err, ErrInvalidConsumerProfileProjection) {
		t.Fatalf("GetMine(invalid projection) error = %v", err)
	}
}

func TestConsumerProfileServiceReservesCanonicalFieldsBeforeModeration(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(21)
	principalID := apiUUID(22)
	operationKey := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	reader := &fakeConsumerProfileReader{openID: "openid-private"}
	writer := &fakeConsumerProfileWriter{replayErr: consumerprofilepostgres.ErrOperationNotFound}
	checker := &fakeConsumerProfileChecker{result: contentsecurity.Result{
		Suggest: contentsecurity.SuggestPass,
		TraceID: "trace-pass",
	}}
	service, err := NewConsumerProfileService(
		tenantID,
		consumerProfileTestAppID,
		"privacy-v1",
		reader,
		writer,
		checker,
	)
	if err != nil {
		t.Fatalf("NewConsumerProfileService() error = %v", err)
	}
	result, err := service.Update(
		context.Background(),
		principalID,
		ConsumerProfileUpdateRequest{
			OperationKey:    operationKey,
			ExpectedVersion: 0,
			Fields: consumerprofile.Fields{
				Occupation:   "  产品设计  ",
				Introduction: "  喜欢线下活动  ",
				Tags:         []string{" 徒步 ", "咖啡"},
				Visibility:   consumerprofile.Visibility{Occupation: true},
			},
		},
	)
	if err != nil || result.ModerationStatus != consumerprofile.ModerationStatusPendingReview ||
		result.PublishedVersion != 0 || writer.applyCalls != 1 ||
		writer.outcome.Status != consumerprofile.ModerationStatusPendingReview ||
		writer.outcome.Source != consumerprofile.ModerationSourceProviderUnavailable ||
		writer.outcome.Observation != nil ||
		writer.patch.TenantID != tenantID || writer.patch.PrincipalID != principalID ||
		writer.patch.OperationKey != operationKey ||
		writer.patch.Fields.Occupation != "产品设计" ||
		checker.calls != 0 || reader.providerCalls != 0 {
		t.Fatalf("Update()=%+v,%v reader=%+v writer=%+v checker=%+v", result, err, reader, writer, checker)
	}
}

func TestConsumerProfileServicePreservesPublishedVersionForReview(t *testing.T) {
	t.Parallel()

	writer := &fakeConsumerProfileWriter{replayErr: consumerprofilepostgres.ErrOperationNotFound}
	service := newTestConsumerProfileService(
		t,
		&fakeConsumerProfileReader{openID: "openid-review"},
		writer,
		&fakeConsumerProfileChecker{result: contentsecurity.Result{
			Suggest: contentsecurity.SuggestReview,
			TraceID: "trace-review",
		}},
		"privacy-v1",
	)
	result, err := service.Update(
		context.Background(),
		apiUUID(23),
		validConsumerProfileUpdateRequest(),
	)
	if err != nil || result.ModerationStatus !=
		consumerprofile.ModerationStatusPendingReview ||
		result.PublishedVersion != 0 || writer.outcome.Status !=
		consumerprofile.ModerationStatusPendingReview {
		t.Fatalf("Update(review) = %+v, %v; writer=%+v", result, err, writer)
	}
}

func TestConsumerProfileServicePersistsRetryableCandidateWhenProviderIsUnavailable(
	t *testing.T,
) {
	t.Parallel()

	tests := []struct {
		name    string
		reader  *fakeConsumerProfileReader
		checker contentsecurity.Checker
	}{
		{
			name:    "provider error",
			reader:  &fakeConsumerProfileReader{openID: "openid-error"},
			checker: &fakeConsumerProfileChecker{err: errors.New("private provider request")},
		},
		{
			name:    "unknown verdict",
			reader:  &fakeConsumerProfileReader{openID: "openid-unknown"},
			checker: &fakeConsumerProfileChecker{result: contentsecurity.Result{Suggest: "unknown"}},
		},
		{
			name:   "pass without provider trace",
			reader: &fakeConsumerProfileReader{openID: "openid-no-trace"},
			checker: &fakeConsumerProfileChecker{result: contentsecurity.Result{
				Suggest: contentsecurity.SuggestPass,
			}},
		},
		{
			name:    "disabled checker",
			reader:  &fakeConsumerProfileReader{openID: "openid-disabled"},
			checker: contentsecurity.DisabledChecker{},
		},
		{
			name: "provider identity unavailable",
			reader: &fakeConsumerProfileReader{
				providerErr: consumerprofilepostgres.ErrProviderIdentityUnavailable,
			},
			checker: &fakeConsumerProfileChecker{},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			writer := &fakeConsumerProfileWriter{replayErr: consumerprofilepostgres.ErrOperationNotFound}
			service := newTestConsumerProfileService(
				t,
				test.reader,
				writer,
				test.checker,
				"privacy-v1",
			)
			receipt, err := service.Update(
				context.Background(),
				apiUUID(24),
				validConsumerProfileUpdateRequest(),
			)
			if err != nil || writer.applyCalls != 1 ||
				receipt.ModerationStatus != consumerprofile.ModerationStatusPendingReview ||
				writer.outcome.Source != consumerprofile.ModerationSourceProviderUnavailable ||
				writer.outcome.Observation != nil {
				t.Fatalf(
					"Update()=%+v,%v apply_calls=%d outcome=%+v",
					receipt,
					err,
					writer.applyCalls,
					writer.outcome,
				)
			}
		})
	}
}

func TestConsumerProfileServiceDoesNotCallProviderBeforeReservation(t *testing.T) {
	t.Parallel()

	writer := &fakeConsumerProfileWriter{
		replayErr: consumerprofilepostgres.ErrOperationNotFound,
	}
	service := newTestConsumerProfileService(
		t,
		&fakeConsumerProfileReader{openID: "openid-risky"},
		writer,
		&fakeConsumerProfileChecker{result: contentsecurity.Result{
			Suggest: contentsecurity.SuggestRisky,
			Label:   100,
			TraceID: "trace-risky",
		}},
		"privacy-v1",
	)
	receipt, err := service.Update(
		context.Background(),
		apiUUID(24),
		validConsumerProfileUpdateRequest(),
	)
	if err != nil || writer.applyCalls != 1 ||
		receipt.ModerationStatus != consumerprofile.ModerationStatusPendingReview ||
		writer.outcome.Status != consumerprofile.ModerationStatusPendingReview ||
		writer.outcome.Source != consumerprofile.ModerationSourceProviderUnavailable {
		t.Fatalf("Update(risky fixture) receipt=%+v error=%v writer=%+v", receipt, err, writer)
	}
}

func TestConsumerProfileServiceRequiresPublishedPrivacyPolicyBeforeWrite(
	t *testing.T,
) {
	t.Parallel()

	writer := &fakeConsumerProfileWriter{
		replayErr: consumerprofilepostgres.ErrOperationNotFound,
	}
	service := newTestConsumerProfileService(
		t,
		&fakeConsumerProfileReader{openID: "openid-policy"},
		writer,
		&fakeConsumerProfileChecker{},
		"",
	)
	_, err := service.Update(
		context.Background(),
		apiUUID(24),
		validConsumerProfileUpdateRequest(),
	)
	if !errors.Is(err, ErrConsumerProfilePolicyUnavailable) ||
		writer.applyCalls != 0 {
		t.Fatalf("Update(no policy) error=%v writer=%+v", err, writer)
	}
}

func TestConsumerProfileServiceReplaysBeforeProviderCall(t *testing.T) {
	t.Parallel()

	request := validConsumerProfileUpdateRequest()
	receipt := consumerProfileReceipt(request, consumerprofile.ModerationStatusApproved)
	receipt.Replayed = true
	reader := &fakeConsumerProfileReader{}
	writer := &fakeConsumerProfileWriter{replay: receipt}
	checker := &fakeConsumerProfileChecker{}
	service := newTestConsumerProfileService(t, reader, writer, checker, "privacy-v1")
	got, err := service.Update(context.Background(), apiUUID(24), request)
	if err != nil || !got.Replayed || reader.providerCalls != 0 ||
		checker.calls != 0 || writer.applyCalls != 0 {
		t.Fatalf("Update(replay)=%+v,%v reader=%+v writer=%+v checker=%+v", got, err, reader, writer, checker)
	}
}

func TestConsumerProfileServiceReplaysDurableRejectionWithoutProviderCall(
	t *testing.T,
) {
	t.Parallel()

	request := validConsumerProfileUpdateRequest()
	receipt := consumerProfileReceipt(
		request,
		consumerprofile.ModerationStatusRejected,
	)
	receipt.Replayed = true
	reader := &fakeConsumerProfileReader{}
	writer := &fakeConsumerProfileWriter{replay: receipt}
	checker := &fakeConsumerProfileChecker{}
	service := newTestConsumerProfileService(
		t,
		reader,
		writer,
		checker,
		"privacy-v1",
	)
	_, err := service.Update(context.Background(), apiUUID(24), request)
	if !errors.Is(err, ErrConsumerProfileContentRejected) ||
		reader.providerCalls != 0 || checker.calls != 0 || writer.applyCalls != 0 {
		t.Fatalf(
			"Update(rejected replay) error=%v reader=%+v writer=%+v checker=%+v",
			err,
			reader,
			writer,
			checker,
		)
	}
}

func TestConsumerProfileServiceAllowsDataClearingWithoutProvider(t *testing.T) {
	t.Parallel()

	request := validConsumerProfileUpdateRequest()
	request.Fields = consumerprofile.Fields{}
	writer := &fakeConsumerProfileWriter{replayErr: consumerprofilepostgres.ErrOperationNotFound}
	reader := &fakeConsumerProfileReader{providerErr: consumerprofilepostgres.ErrProviderIdentityUnavailable}
	checker := &fakeConsumerProfileChecker{err: errors.New("must not run")}
	service := newTestConsumerProfileService(t, reader, writer, checker, "privacy-v1")
	got, err := service.Update(context.Background(), apiUUID(24), request)
	if err != nil || got.PublishedVersion != request.ExpectedVersion+1 ||
		reader.providerCalls != 0 || checker.calls != 0 || writer.applyCalls != 1 {
		t.Fatalf("Update(clear)=%+v,%v reader=%+v writer=%+v checker=%+v", got, err, reader, writer, checker)
	}
}

func newTestConsumerProfileService(
	t *testing.T,
	reader *fakeConsumerProfileReader,
	writer *fakeConsumerProfileWriter,
	checker contentsecurity.Checker,
	policy string,
) *ConsumerProfileService {
	t.Helper()
	service, err := NewConsumerProfileService(
		apiUUID(21),
		consumerProfileTestAppID,
		policy,
		reader,
		writer,
		checker,
	)
	if err != nil {
		t.Fatalf("NewConsumerProfileService() error = %v", err)
	}
	return service
}

func validConsumerProfileUpdateRequest() ConsumerProfileUpdateRequest {
	return ConsumerProfileUpdateRequest{
		OperationKey:    uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
		ExpectedVersion: 2,
		Fields: consumerprofile.Fields{
			Occupation:   "产品设计",
			Introduction: "喜欢线下活动",
			Tags:         []string{"徒步", "咖啡"},
			Visibility:   consumerprofile.Visibility{Occupation: true},
		},
	}
}

func consumerProfileReceipt(
	request ConsumerProfileUpdateRequest,
	status consumerprofile.ModerationStatus,
) consumerprofile.MutationReceipt {
	publishedVersion := int64(0)
	if status == consumerprofile.ModerationStatusApproved {
		publishedVersion = request.ExpectedVersion + 1
	}
	return consumerprofile.MutationReceipt{
		CandidateID:      apiUUID(25),
		Fields:           request.Fields,
		BaseVersion:      request.ExpectedVersion,
		CandidateVersion: request.ExpectedVersion + 1,
		ModerationStatus: status,
		PublishedVersion: publishedVersion,
		PrivacyVersion:   "privacy-v1",
		SubmittedAt:      time.Date(2026, 9, 14, 4, 5, 6, 0, time.UTC),
	}
}

type fakeConsumerProfileReader struct {
	snapshot      consumerprofile.Snapshot
	err           error
	openID        string
	providerErr   error
	readCalls     int
	providerCalls int
	tenantID      uuid.UUID
	principalID   uuid.UUID
}

func (reader *fakeConsumerProfileReader) ReadMine(
	_ context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) (consumerprofile.Snapshot, error) {
	reader.readCalls++
	reader.tenantID = tenantID
	reader.principalID = principalID
	return reader.snapshot, reader.err
}

func (reader *fakeConsumerProfileReader) ProviderOpenID(
	_ context.Context,
	_ uuid.UUID,
	_ string,
) (string, error) {
	reader.providerCalls++
	return reader.openID, reader.providerErr
}

type fakeConsumerProfileWriter struct {
	replay     consumerprofile.MutationReceipt
	replayErr  error
	applyErr   error
	patch      consumerprofile.Patch
	outcome    consumerprofile.ModerationOutcome
	applyCalls int
}

func (writer *fakeConsumerProfileWriter) Replay(
	_ context.Context,
	patch consumerprofile.Patch,
) (consumerprofile.MutationReceipt, error) {
	writer.patch = patch
	return writer.replay, writer.replayErr
}

func (writer *fakeConsumerProfileWriter) Apply(
	_ context.Context,
	patch consumerprofile.Patch,
	outcome consumerprofile.ModerationOutcome,
) (consumerprofile.MutationReceipt, error) {
	writer.applyCalls++
	writer.patch = patch
	writer.outcome = outcome
	if writer.applyErr != nil {
		return consumerprofile.MutationReceipt{}, writer.applyErr
	}
	request := ConsumerProfileUpdateRequest{
		ExpectedVersion: patch.ExpectedVersion,
		Fields:          patch.Fields,
	}
	return consumerProfileReceipt(request, outcome.Status), nil
}

type fakeConsumerProfileChecker struct {
	result contentsecurity.Result
	err    error
	openID string
	text   string
	scene  int
	calls  int
}

func (checker *fakeConsumerProfileChecker) CheckText(
	_ context.Context,
	_ string,
	openID string,
	text string,
	scene int,
) (contentsecurity.Result, error) {
	checker.calls++
	checker.openID = openID
	checker.text = text
	checker.scene = scene
	return checker.result, checker.err
}
