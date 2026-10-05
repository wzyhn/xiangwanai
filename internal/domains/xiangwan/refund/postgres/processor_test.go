package refundpostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestProcessorStartsManualWorkWithImmutableEvent(t *testing.T) {
	t.Parallel()

	current := pendingRefundCase(t)
	order := paidOrderForRefundCase(current)
	actorID := uuid.New()
	command := StartProcessingCommand{
		TenantID:       current.TenantID,
		RefundCaseID:   current.ID,
		ActorID:        actorID,
		IdempotencyKey: "refund-operation:start-1",
		OperatorNote:   "opened merchant console",
		OccurredAt:     current.CreatedAt.Add(time.Minute),
	}
	processor, tx, capture := newRefundProcessorHarness(
		current,
		order,
		refundProcessorScenario{},
		command.OccurredAt.Add(time.Second),
	)
	result, err := processor.StartProcessing(context.Background(), command)
	if err != nil {
		t.Fatalf("StartProcessing() error = %v", err)
	}
	if result.Case.RefundStatus != refund.StatusProcessing ||
		result.Event.EventType != refund.EventTypeProcessingStarted ||
		result.Event.ActorID != actorID ||
		result.Event.IdempotencyKey != command.IdempotencyKey ||
		result.Event.ResultingRefundVersion != result.Case.Version {
		t.Fatalf("StartProcessing() = %+v", result)
	}
	if !tx.committed || tx.rolledBack || capture.isolation != sql.LevelSerializable ||
		capture.refundUpdates != 1 || capture.eventCreates != 1 || capture.eventLookups != 1 ||
		!reflect.DeepEqual(capture.lockOrder, []string{"order", "refund"}) {
		t.Fatalf("transaction/capture = %+v %+v", tx, capture)
	}
}

func TestProcessorCompletesExactAmountWithExternalEvidence(t *testing.T) {
	t.Parallel()

	current := pendingRefundCase(t)
	processingAt := current.CreatedAt.Add(time.Minute)
	current, _, _ = refund.StartProcessing(current, refund.StartProcessingCommand{
		HandledBy: uuid.New(),
		At:        processingAt,
	})
	order := paidOrderForRefundCase(current)
	command := CompleteRefundCommand{
		TenantID:              current.TenantID,
		RefundCaseID:          current.ID,
		ActorID:               uuid.New(),
		IdempotencyKey:        "refund-operation:complete-1",
		SuccessfulRefundCents: current.RequestedRefundCents,
		ExternalRefundID:      "wx-refund-complete-1",
		EvidenceReference:     "merchant-console/refunds/complete-1",
		OperatorNote:          "response signature verified",
		OccurredAt:            processingAt.Add(time.Minute),
	}
	processor, tx, capture := newRefundProcessorHarness(
		current,
		order,
		refundProcessorScenario{},
		command.OccurredAt.Add(time.Second),
	)
	result, err := processor.Complete(context.Background(), command)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if result.Case.RefundStatus != refund.StatusRefunded ||
		result.Case.SuccessfulRefundCents != current.RequestedRefundCents ||
		result.Event.EventType != refund.EventTypeRefundCompleted ||
		result.Event.ExternalRefundID == nil ||
		*result.Event.ExternalRefundID != command.ExternalRefundID ||
		!tx.committed || capture.refundUpdates != 1 || capture.eventCreates != 1 {
		t.Fatalf("Complete() = %+v transaction=%+v capture=%+v", result, tx, capture)
	}
}

func TestProcessorAppliesConfiguredCouponPolicyOnFullRefund(t *testing.T) {
	t.Parallel()

	current := pendingRefundCase(t)
	order := paidOrderForRefundCase(current)
	ledger := redeemedRefundCouponLedger(t, order, current.CreatedAt)
	order.OriginalPriceCents += ledger.Instrument.FaceValueCents
	order.DiscountCents = ledger.Instrument.FaceValueCents
	command := CompleteRefundCommand{
		TenantID:              current.TenantID,
		RefundCaseID:          current.ID,
		ActorID:               uuid.New(),
		IdempotencyKey:        "refund-operation:coupon-restore",
		SuccessfulRefundCents: current.RequestedRefundCents,
		ExternalRefundID:      "wx-refund-coupon-restore",
		OccurredAt:            current.CreatedAt.Add(time.Minute),
	}
	processor, tx, capture := newRefundProcessorHarness(
		current,
		order,
		refundProcessorScenario{couponLedger: &ledger},
		command.OccurredAt.Add(time.Second),
	)
	policy := &fakeCouponRefundPolicy{
		decision: coupon.RefundPolicyDecision{
			Configured:    true,
			PolicyVersion: "coupon-refund-v1",
			Disposition:   coupon.RefundDispositionRestore,
		},
	}
	processor.couponPolicy = policy

	result, err := processor.Complete(context.Background(), command)
	if err != nil {
		t.Fatalf("Complete(Coupon) error = %v", err)
	}
	if result.CouponAdjustment == nil ||
		result.CouponAdjustment.EntryType != coupon.EntryTypeRestored ||
		result.CouponAdjustment.RefundCaseID == nil ||
		*result.CouponAdjustment.RefundCaseID != current.ID ||
		result.CouponAdjustment.RefundPolicyVersion == nil ||
		*result.CouponAdjustment.RefundPolicyVersion != "coupon-refund-v1" ||
		policy.calls != 1 || capture.couponAppends != 1 ||
		!tx.committed || tx.rolledBack {
		t.Fatalf(
			"Complete(Coupon) = %+v policy=%+v tx=%+v capture=%+v",
			result,
			policy,
			tx,
			capture,
		)
	}
	replayedLedger := ledger
	replayedLedger.Entries = append(
		replayedLedger.Entries,
		*result.CouponAdjustment,
	)
	replayProcessor, replayTx, replayCapture := newRefundProcessorHarness(
		result.Case,
		order,
		refundProcessorScenario{
			existingEvent: &result.Event,
			couponLedger:  &replayedLedger,
		},
		command.OccurredAt.Add(2*time.Second),
	)
	replayed, err := replayProcessor.Complete(context.Background(), command)
	if err != nil || replayed.CouponAdjustment == nil ||
		replayed.CouponAdjustment.ID != result.CouponAdjustment.ID ||
		!replayTx.committed || replayTx.rolledBack ||
		replayCapture.couponAppends != 0 {
		t.Fatalf(
			"Complete(Coupon replay) = %+v error=%v tx=%+v capture=%+v",
			replayed,
			err,
			replayTx,
			replayCapture,
		)
	}
}

func TestProcessorFailsClosedWithoutCouponRefundPolicy(t *testing.T) {
	t.Parallel()

	current := pendingRefundCase(t)
	order := paidOrderForRefundCase(current)
	ledger := redeemedRefundCouponLedger(t, order, current.CreatedAt)
	order.OriginalPriceCents += ledger.Instrument.FaceValueCents
	order.DiscountCents = ledger.Instrument.FaceValueCents
	command := CompleteRefundCommand{
		TenantID:              current.TenantID,
		RefundCaseID:          current.ID,
		ActorID:               uuid.New(),
		IdempotencyKey:        "refund-operation:coupon-policy-missing",
		SuccessfulRefundCents: current.RequestedRefundCents,
		ExternalRefundID:      "wx-refund-coupon-policy-missing",
		OccurredAt:            current.CreatedAt.Add(time.Minute),
	}
	processor, tx, capture := newRefundProcessorHarness(
		current,
		order,
		refundProcessorScenario{couponLedger: &ledger},
		command.OccurredAt.Add(time.Second),
	)
	if _, err := processor.Complete(
		context.Background(),
		command,
	); !errors.Is(err, ErrCouponRefundPolicyUnavailable) {
		t.Fatalf("Complete(missing Coupon policy) error = %v", err)
	}
	if tx.committed || !tx.rolledBack || capture.couponAppends != 0 {
		t.Fatalf("transaction=%+v capture=%+v", tx, capture)
	}
}

func TestProcessorRecordsFailureAndRejectionWithoutRestoringParticipation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		action   refundOperationAction
		wantType refund.EventType
		want     refund.Status
	}{
		{name: "failure", action: refundOperationFail, wantType: refund.EventTypeRefundFailed, want: refund.StatusFailed},
		{name: "rejection", action: refundOperationReject, wantType: refund.EventTypeRefundRejected, want: refund.StatusRejected},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			current := pendingRefundCase(t)
			order := paidOrderForRefundCase(current)
			command := ResolveRefundCommand{
				TenantID:       current.TenantID,
				RefundCaseID:   current.ID,
				ActorID:        uuid.New(),
				IdempotencyKey: "refund-operation:" + test.name,
				FailureReason:  "manual evidence " + test.name,
				OperatorNote:   "escalated",
				OccurredAt:     current.CreatedAt.Add(time.Minute),
			}
			processor, tx, capture := newRefundProcessorHarness(
				current,
				order,
				refundProcessorScenario{},
				command.OccurredAt.Add(time.Second),
			)
			var result RefundOperationResult
			var err error
			if test.action == refundOperationFail {
				result, err = processor.Fail(context.Background(), command)
			} else {
				result, err = processor.Reject(context.Background(), command)
			}
			if err != nil {
				t.Fatalf("resolve() error = %v", err)
			}
			if result.Case.RefundStatus != test.want ||
				result.Event.EventType != test.wantType ||
				result.Event.FailureReason == nil ||
				*result.Event.FailureReason != command.FailureReason ||
				!tx.committed || capture.refundUpdates != 1 || capture.eventCreates != 1 {
				t.Fatalf("resolve() = %+v transaction=%+v capture=%+v", result, tx, capture)
			}
		})
	}
}

func TestProcessorReplaysEventWhileReturningCurrentCaseView(t *testing.T) {
	t.Parallel()

	current := pendingRefundCase(t)
	order := paidOrderForRefundCase(current)
	command := CompleteRefundCommand{
		TenantID:              current.TenantID,
		RefundCaseID:          current.ID,
		ActorID:               uuid.New(),
		IdempotencyKey:        "refund-operation:complete-replay",
		SuccessfulRefundCents: current.RequestedRefundCents,
		ExternalRefundID:      "wx-refund-replay-1",
		EvidenceReference:     "merchant-console/refunds/replay-1",
		OperatorNote:          "verified",
		OccurredAt:            current.CreatedAt.Add(time.Minute),
	}
	completed, _, err := refund.Complete(current, refund.CompleteCommand{
		HandledBy:         command.ActorID,
		ExternalRefundID:  command.ExternalRefundID,
		EvidenceReference: command.EvidenceReference,
		OperatorNote:      command.OperatorNote,
		At:                command.OccurredAt,
	})
	if err != nil {
		t.Fatalf("Complete(domain) error = %v", err)
	}
	event, err := refund.NewEvent(
		current,
		completed,
		command.IdempotencyKey,
		command.OccurredAt.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	processor, tx, capture := newRefundProcessorHarness(
		completed,
		order,
		refundProcessorScenario{existingEvent: &event},
		command.OccurredAt.Add(2*time.Second),
	)
	result, err := processor.Complete(context.Background(), command)
	if err != nil {
		t.Fatalf("Complete(replay) error = %v", err)
	}
	if result.Event.ID != event.ID || result.Case.ID != completed.ID ||
		!tx.committed || tx.rolledBack || capture.refundUpdates != 0 ||
		capture.eventCreates != 0 || capture.eventLookups != 1 {
		t.Fatalf("Complete(replay) = %+v transaction=%+v capture=%+v", result, tx, capture)
	}

	conflicting := command
	conflicting.OperatorNote = "rewritten"
	processor, tx, _ = newRefundProcessorHarness(
		completed,
		order,
		refundProcessorScenario{existingEvent: &event},
		command.OccurredAt.Add(2*time.Second),
	)
	if _, err := processor.Complete(context.Background(), conflicting); !errors.Is(
		err,
		ErrRefundOperationConflict,
	) {
		t.Fatalf("Complete(conflicting replay) error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("conflicting replay transaction = %+v", tx)
	}
}

func TestProcessorRejectsMismatchedAmountAndUnpaidOrder(t *testing.T) {
	t.Parallel()

	current := pendingRefundCase(t)
	order := paidOrderForRefundCase(current)
	command := CompleteRefundCommand{
		TenantID:              current.TenantID,
		RefundCaseID:          current.ID,
		ActorID:               uuid.New(),
		IdempotencyKey:        "refund-operation:bad-amount",
		SuccessfulRefundCents: current.RequestedRefundCents - 1,
		ExternalRefundID:      "wx-refund-bad-amount",
		OccurredAt:            current.CreatedAt.Add(time.Minute),
	}
	processor, tx, capture := newRefundProcessorHarness(
		current,
		order,
		refundProcessorScenario{},
		command.OccurredAt.Add(time.Second),
	)
	if _, err := processor.Complete(context.Background(), command); !errors.Is(
		err,
		ErrRefundOperationConflict,
	) {
		t.Fatalf("Complete(amount mismatch) error = %v", err)
	}
	if tx.committed || !tx.rolledBack || capture.refundUpdates != 0 {
		t.Fatalf("amount mismatch transaction/capture = %+v %+v", tx, capture)
	}

	unpaid := order
	unpaid.PaymentStatus = payment.OrderStatusClosedUnpaid
	unpaid.ActualPaidCents = nil
	processor, tx, _ = newRefundProcessorHarness(
		current,
		unpaid,
		refundProcessorScenario{},
		command.OccurredAt.Add(time.Second),
	)
	if _, err := processor.Complete(context.Background(), command); !errors.Is(
		err,
		ErrRefundOperationTransaction,
	) {
		t.Fatalf("Complete(unpaid Order) error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("unpaid transaction = %+v", tx)
	}
}

func TestProcessorValidatesBeforeLookupAndTranslatesMissing(t *testing.T) {
	t.Parallel()

	tx := &fakeRefundOperationTransaction{}
	starter := &fakeRefundOperationTransactionStarter{tx: tx}
	processor := &Processor{transactions: starter, now: time.Now}
	if _, err := processor.StartProcessing(
		context.Background(),
		StartProcessingCommand{},
	); !errors.Is(err, ErrInvalidRefundOperationCommand) {
		t.Fatalf("StartProcessing(invalid) error = %v", err)
	}
	if tx.locates != 0 || starter.begins != 0 {
		t.Fatalf("invalid transaction/starter = %+v %+v", tx, starter)
	}

	tx.locateErr = ErrRefundCaseNotFound
	if _, err := processor.StartProcessing(context.Background(), StartProcessingCommand{
		TenantID:       uuid.New(),
		RefundCaseID:   uuid.New(),
		ActorID:        uuid.New(),
		IdempotencyKey: "refund-operation:missing",
		OccurredAt:     time.Now(),
	}); !errors.Is(err, ErrRefundOperationNotFound) {
		t.Fatalf("StartProcessing(missing) error = %v", err)
	}
	if starter.begins != 1 || !starter.tx.rolledBack {
		t.Fatalf("missing target transaction = %+v", starter)
	}
}

func TestProcessorRequiresDistinctPriorHandlerAndExactVersionForAdminCompletion(t *testing.T) {
	t.Parallel()
	pending := pendingRefundCase(t)
	starterID := uuid.New()
	current, _, err := refund.StartProcessing(pending, refund.StartProcessingCommand{
		HandledBy: starterID, At: pending.CreatedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	order := paidOrderForRefundCase(current)
	command := CompleteRefundCommand{
		TenantID: current.TenantID, RefundCaseID: current.ID,
		ActorID: starterID, ExpectedVersion: current.Version,
		RequireDistinctPriorHandler: true,
		IdempotencyKey:              uuid.NewString(), SuccessfulRefundCents: current.RequestedRefundCents,
		ExternalRefundID: "official-refund-1", EvidenceReference: "merchant-record-1",
		OccurredAt: current.UpdatedAt.Add(time.Minute),
	}
	processor, tx, capture := newRefundProcessorHarness(current, order,
		refundProcessorScenario{}, command.OccurredAt.Add(time.Second))
	if _, err := processor.Complete(context.Background(), command); !errors.Is(err, ErrRefundOperationConflict) ||
		!tx.rolledBack || capture.refundUpdates != 0 {
		t.Fatalf("same-actor completion = %v tx=%+v capture=%+v", err, tx, capture)
	}
	command.ActorID = uuid.New()
	command.ExpectedVersion--
	processor, tx, capture = newRefundProcessorHarness(current, order,
		refundProcessorScenario{}, command.OccurredAt.Add(time.Second))
	if _, err := processor.Complete(context.Background(), command); !errors.Is(err, ErrRefundOperationConflict) ||
		!tx.rolledBack || capture.refundUpdates != 0 {
		t.Fatalf("stale-version completion = %v tx=%+v capture=%+v", err, tx, capture)
	}
	command.ExpectedVersion = current.Version
	processor, tx, _ = newRefundProcessorHarness(current, order,
		refundProcessorScenario{}, command.OccurredAt.Add(time.Second))
	result, err := processor.Complete(context.Background(), command)
	if err != nil || result.Case.RefundStatus != refund.StatusRefunded || !tx.committed {
		t.Fatalf("independent completion = %+v, %v tx=%+v", result, err, tx)
	}
}

func TestProcessorRequiresActiveProcessingBeforeAdminFailure(t *testing.T) {
	t.Parallel()
	pending := pendingRefundCase(t)
	command := ResolveRefundCommand{
		TenantID: pending.TenantID, RefundCaseID: pending.ID,
		ActorID: uuid.New(), ExpectedVersion: pending.Version,
		RequireProcessing: true, IdempotencyKey: uuid.NewString(),
		FailureReason: "merchant channel failed", OccurredAt: pending.CreatedAt.Add(time.Minute),
	}
	processor, tx, capture := newRefundProcessorHarness(pending, paidOrderForRefundCase(pending),
		refundProcessorScenario{}, command.OccurredAt.Add(time.Second))
	if _, err := processor.Fail(context.Background(), command); !errors.Is(err, ErrRefundOperationConflict) ||
		!tx.rolledBack || capture.refundUpdates != 0 {
		t.Fatalf("fail before processing = %v tx=%+v capture=%+v", err, tx, capture)
	}
}

func TestRefundOperationErrorsAreClassified(t *testing.T) {
	t.Parallel()

	if got := classifyRefundOperationWriteError(ErrRefundCaseVersionConflict); !errors.Is(
		got,
		ErrRefundOperationTransaction,
	) {
		t.Fatalf("classify version = %v", got)
	}
	if got := classifyRefundOperationWriteError(&pgconn.PgError{Code: "23505"}); !errors.Is(
		got,
		ErrRefundOperationConflict,
	) {
		t.Fatalf("classify unique = %v", got)
	}
	if got := classifyRefundOperationCommitError(&pgconn.PgError{Code: "40001"}); !errors.Is(
		got,
		ErrRefundOperationTransaction,
	) {
		t.Fatalf("classify serialization = %v", got)
	}
}

type refundProcessorScenario struct {
	existingEvent *refund.Event
	couponLedger  *couponpostgres.Ledger
	couponErr     error
	commitErr     error
}

type refundProcessorCapture struct {
	isolation     sql.IsolationLevel
	lockOrder     []string
	eventLookups  int
	refundUpdates int
	eventCreates  int
	couponAppends int
}

func newRefundProcessorHarness(
	current refund.Case,
	order payment.Order,
	scenario refundProcessorScenario,
	recordedAt time.Time,
) (*Processor, *fakeRefundOperationTransaction, *refundProcessorCapture) {
	capture := &refundProcessorCapture{}
	tx := &fakeRefundOperationTransaction{
		current:  current,
		order:    order,
		scenario: scenario,
		capture:  capture,
	}
	return &Processor{
		transactions: &fakeRefundOperationTransactionStarter{
			tx:      tx,
			capture: capture,
		},
		now: func() time.Time { return recordedAt },
	}, tx, capture
}

type fakeRefundOperationTransactionStarter struct {
	tx      *fakeRefundOperationTransaction
	capture *refundProcessorCapture
	err     error
	begins  int
}

func (starter *fakeRefundOperationTransactionStarter) beginRefundOperationTx(
	_ context.Context,
	options *sql.TxOptions,
	_ uuid.UUID,
	_ uuid.UUID,
	_ string,
) (refundOperationTransaction, error) {
	starter.begins++
	if starter.capture != nil {
		starter.capture.isolation = options.Isolation
	}
	return starter.tx, starter.err
}

type fakeRefundOperationTransaction struct {
	current    refund.Case
	locateErr  error
	locates    int
	order      payment.Order
	scenario   refundProcessorScenario
	capture    *refundProcessorCapture
	committed  bool
	rolledBack bool
}

func (tx *fakeRefundOperationTransaction) locateRefund(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (refund.Case, error) {
	tx.locates++
	return tx.current, tx.locateErr
}

func (tx *fakeRefundOperationTransaction) lockOrder(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (payment.Order, error) {
	tx.capture.lockOrder = append(tx.capture.lockOrder, "order")
	return tx.order, nil
}

func (tx *fakeRefundOperationTransaction) lockRefund(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (refund.Case, error) {
	tx.capture.lockOrder = append(tx.capture.lockOrder, "refund")
	return tx.current, nil
}

func (tx *fakeRefundOperationTransaction) lockCouponByOrder(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (couponpostgres.Ledger, error) {
	if tx.scenario.couponLedger == nil {
		return couponpostgres.Ledger{}, tx.scenario.couponErr
	}
	return *tx.scenario.couponLedger, tx.scenario.couponErr
}

func (tx *fakeRefundOperationTransaction) getEventByIdempotencyKey(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	string,
) (refund.Event, error) {
	tx.capture.eventLookups++
	if tx.scenario.existingEvent == nil {
		return refund.Event{}, errRefundOperationEventNotFound
	}
	return *tx.scenario.existingEvent, nil
}

func (tx *fakeRefundOperationTransaction) updateRefund(
	_ context.Context,
	value refund.Case,
	_ int64,
) (refund.Case, error) {
	tx.capture.refundUpdates++
	tx.current = value
	return value, nil
}

func (tx *fakeRefundOperationTransaction) createEvent(
	_ context.Context,
	value refund.Event,
) (refund.Event, error) {
	tx.capture.eventCreates++
	return value, nil
}

func (tx *fakeRefundOperationTransaction) appendCouponEntry(
	_ context.Context,
	value coupon.Entry,
) (coupon.Entry, error) {
	tx.capture.couponAppends++
	return value, nil
}

func (tx *fakeRefundOperationTransaction) Commit() error {
	if tx.scenario.commitErr != nil {
		return tx.scenario.commitErr
	}
	tx.committed = true
	return nil
}

func (tx *fakeRefundOperationTransaction) Rollback() error {
	tx.rolledBack = true
	return nil
}

func paidOrderForRefundCase(value refund.Case) payment.Order {
	paidCents := value.RequestedRefundCents
	transactionID := "wx-transaction-" + value.OrderID.String()
	paidAt := value.CreatedAt
	return payment.Order{
		ID:                  value.OrderID,
		TenantID:            value.TenantID,
		RegistrationID:      value.RegistrationID,
		SeriesID:            value.SeriesID,
		InstanceID:          value.InstanceID,
		SessionID:           value.SessionID,
		PrincipalID:         value.PrincipalID,
		PaymentStatus:       payment.OrderStatusPaidConfirmed,
		IdempotencyKey:      "payment:refund-operation",
		MerchantOrderNo:     "merchant-refund-operation",
		PaymentAppID:        "wx-app-refund-operation",
		PaymentMerchantID:   "wx-merchant-refund-operation",
		OriginalPriceCents:  paidCents,
		PayableCents:        paidCents,
		ActualPaidCents:     &paidCents,
		WeChatTransactionID: &transactionID,
		PaidAt:              &paidAt,
		Version:             2,
		CreatedAt:           value.CreatedAt,
		UpdatedAt:           value.CreatedAt,
	}
}

type fakeCouponRefundPolicy struct {
	decision coupon.RefundPolicyDecision
	err      error
	input    coupon.RefundPolicyInput
	calls    int
}

func (policy *fakeCouponRefundPolicy) EvaluateCouponRefund(
	_ context.Context,
	input coupon.RefundPolicyInput,
) (coupon.RefundPolicyDecision, error) {
	policy.calls++
	policy.input = input
	return policy.decision, policy.err
}

func redeemedRefundCouponLedger(
	t *testing.T,
	order payment.Order,
	at time.Time,
) couponpostgres.Ledger {
	t.Helper()
	activityType := activity.ActivityTypeAIRoundtable
	grant, err := coupon.NewManualReplenishment(
		coupon.ManualReplenishmentCommand{
			TenantID:    order.TenantID,
			PrincipalID: order.PrincipalID,
			ActorID:     uuid.New(),
			BusinessKey: "refund-policy-fixture",
			Reason:      "test grant",
			Context:     "refund processor",
			Policy: coupon.GrantPolicy{
				Configured:        true,
				PolicyVersion:     "grant-v1",
				FaceValueCents:    1_000,
				Validity:          24 * time.Hour,
				ScopeType:         coupon.ScopeTypeActivityType,
				ScopeActivityType: &activityType,
			},
			GrantedAt:  at.Add(-time.Hour),
			RecordedAt: at.Add(-time.Hour),
		},
	)
	if err != nil {
		t.Fatalf("NewManualReplenishment() error = %v", err)
	}
	instrument := grant.Coupons[0]
	order.OriginalPriceCents += instrument.FaceValueCents
	order.DiscountCents = instrument.FaceValueCents
	held, err := coupon.Hold(coupon.HoldCommand{
		Instrument:         instrument,
		History:            []coupon.Entry{grant.Entries[0]},
		OrderID:            order.ID,
		RegistrationID:     order.RegistrationID,
		SeriesID:           order.SeriesID,
		ActivityType:       activityType,
		OriginalPriceCents: order.OriginalPriceCents,
		HoldExpiresAt:      at.Add(time.Minute),
		At:                 at.Add(-time.Minute),
		RecordedAt:         at.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	history := append([]coupon.Entry{grant.Entries[0]}, held)
	redeemed, err := coupon.Redeem(coupon.RedeemCommand{
		Instrument: instrument,
		History:    history,
		OrderID:    order.ID,
		At:         at,
		RecordedAt: at,
	})
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}
	return couponpostgres.Ledger{
		Instrument: instrument,
		Entries:    append(history, redeemed),
	}
}
