package xiangwanruntime

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

func TestProfileModerationWorkerPublishesOnlyDurableProviderPass(t *testing.T) {
	t.Parallel()

	store := newFakeProfileModerationStore()
	checker := &fakeProfileModerationChecker{result: contentsecurity.Result{
		Suggest: contentsecurity.SuggestPass,
		Label:   0,
		TraceID: "trace-worker-pass",
	}}
	observedAt := time.Date(2026, 9, 14, 8, 9, 10, 0, time.UTC)
	worker := &ProfileModerationWorker{
		store:        store,
		checker:      checker,
		tenantID:     runtimeUUID(80),
		generationID: runtimeUUID(81),
		appID:        testRuntimeAppID,
		now:          func() time.Time { return observedAt },
	}
	found, err := worker.processNext(context.Background())
	if err != nil || !found || store.resolveCalls != 1 ||
		checker.openID != "openid-worker" || checker.scene != 1 ||
		store.outcome.Status != consumerprofile.ModerationStatusApproved ||
		store.outcome.Source != consumerprofile.ModerationSourceWeChatTextV2 ||
		store.outcome.Observation == nil ||
		store.outcome.Observation.TraceID != "trace-worker-pass" ||
		store.outcome.Observation.PolicyVersion !=
			consumerprofile.WeChatTextModerationPolicyVersion {
		t.Fatalf(
			"processNext() found=%t err=%v store=%+v checker=%+v",
			found,
			err,
			store,
			checker,
		)
	}
}

func TestProfileModerationWorkerRetainsCandidateOnProviderFailure(t *testing.T) {
	t.Parallel()

	store := newFakeProfileModerationStore()
	checker := &fakeProfileModerationChecker{
		err: errors.New("provider request contained private credentials"),
	}
	worker := &ProfileModerationWorker{
		store:        store,
		checker:      checker,
		tenantID:     runtimeUUID(82),
		generationID: runtimeUUID(83),
		appID:        testRuntimeAppID,
		now: func() time.Time {
			return time.Date(2026, 9, 14, 8, 9, 10, 0, time.UTC)
		},
	}
	found, err := worker.processNext(context.Background())
	if err != nil || !found || store.resolveCalls != 1 ||
		store.outcome.Status != consumerprofile.ModerationStatusPendingReview ||
		store.outcome.Source !=
			consumerprofile.ModerationSourceProviderUnavailable ||
		store.outcome.Observation != nil {
		t.Fatalf("processNext(provider failure)=%t,%v store=%+v", found, err, store)
	}
}

func TestProfileModerationWorkerTreatsExpiredLeaseAsConverged(t *testing.T) {
	t.Parallel()

	store := newFakeProfileModerationStore()
	store.resolveErr = consumerprofilepostgres.ErrModerationLeaseLost
	worker := &ProfileModerationWorker{
		store:        store,
		checker:      &fakeProfileModerationChecker{err: errors.New("unavailable")},
		tenantID:     runtimeUUID(84),
		generationID: runtimeUUID(85),
		appID:        testRuntimeAppID,
		now:          time.Now,
	}
	if found, err := worker.processNext(context.Background()); err != nil || !found {
		t.Fatalf("processNext(expired lease)=%t,%v", found, err)
	}
}

func TestProfileModerationWorkerStopsCleanlyAfterGenerationCutover(t *testing.T) {
	t.Parallel()

	generationGate := &fakeWorkerGenerationGate{err: ErrGenerationInactive}
	worker := &ProfileModerationWorker{
		store:          newFakeProfileModerationStore(),
		checker:        &fakeProfileModerationChecker{},
		generationGate: generationGate,
		schemaGate:     &fakeProfileModerationSchemaGate{},
		tenantID:       runtimeUUID(89),
		generationID:   runtimeUUID(90),
		appID:          testRuntimeAppID,
		now:            time.Now,
		pollInterval:   time.Millisecond,
	}
	if err := worker.Run(context.Background()); err != nil ||
		generationGate.calls != 1 {
		t.Fatalf("Run(inactive generation) calls=%d error=%v", generationGate.calls, err)
	}
}

func newFakeProfileModerationStore() *fakeProfileModerationStore {
	return &fakeProfileModerationStore{
		task: consumerprofile.ModerationTask{
			CandidateID: runtimeUUID(86),
			PrincipalID: runtimeUUID(87),
			LeaseToken:  runtimeUUID(88),
			Attempt:     1,
			Fields: consumerprofile.Fields{
				Occupation: "产品设计",
				Tags:       []string{},
			},
		},
		found:  true,
		openID: "openid-worker",
	}
}

type fakeProfileModerationStore struct {
	task         consumerprofile.ModerationTask
	found        bool
	claimErr     error
	openID       string
	identityErr  error
	resolveErr   error
	outcome      consumerprofile.ModerationOutcome
	resolveCalls int
}

func (store *fakeProfileModerationStore) ClaimModerationTask(
	context.Context,
	uuid.UUID,
) (consumerprofile.ModerationTask, bool, error) {
	return store.task, store.found, store.claimErr
}

func (store *fakeProfileModerationStore) ProviderOpenID(
	context.Context,
	uuid.UUID,
	string,
) (string, error) {
	return store.openID, store.identityErr
}

func (store *fakeProfileModerationStore) ResolveModerationTask(
	_ context.Context,
	_ uuid.UUID,
	_ uuid.UUID,
	_ uuid.UUID,
	outcome consumerprofile.ModerationOutcome,
) error {
	store.resolveCalls++
	store.outcome = outcome
	return store.resolveErr
}

type fakeProfileModerationChecker struct {
	result contentsecurity.Result
	err    error
	openID string
	scene  int
}

type fakeProfileModerationSchemaGate struct {
	err error
}

func (gate *fakeProfileModerationSchemaGate) CheckProfileModerationSchema(
	context.Context,
) error {
	return gate.err
}

func (checker *fakeProfileModerationChecker) CheckText(
	_ context.Context,
	_ string,
	openID string,
	_ string,
	scene int,
) (contentsecurity.Result, error) {
	checker.openID = openID
	checker.scene = scene
	return checker.result, checker.err
}
