package registrationpostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRegistrationCancellerCancelsFreeParticipationAndReleasesCapacity(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	current := confirmedRegistration(now.Add(-time.Hour))
	canceller, tx, capture := newCancellationHarness(
		current,
		cancellationScenario{},
		now,
		nil,
	)
	result, err := canceller.Cancel(context.Background(), selfCancellationCommand(current))
	if err != nil {
		t.Fatalf("Cancel(free) error = %v", err)
	}
	if result.Registration.ParticipationStatus != registration.ParticipationStatusCancelled ||
		result.Registration.CancellationReason == nil ||
		*result.Registration.CancellationReason != "user_cancelled" ||
		result.Order != nil || result.Hold != nil || result.Refund != nil {
		t.Fatalf("Cancel(free) = %+v", result)
	}
	if !tx.committed || tx.rolledBack ||
		capture.registrationUpdates != 1 ||
		capture.confirmedCapacityReleases != 1 ||
		capture.holdCapacityReleases != 0 ||
		capture.refundCreates != 0 ||
		capture.isolation != sql.LevelSerializable {
		t.Fatalf("transaction/capture = %+v %+v", tx, capture)
	}
	if !reflect.DeepEqual(capture.lockOrder, []string{
		"series", "instance", "session", "registration", "order",
	}) {
		t.Fatalf("lock order = %v", capture.lockOrder)
	}
}

func TestRegistrationCancellerRechecksConfiguredCutoffForFreeParticipation(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	current := confirmedRegistration(now.Add(-time.Hour))
	policy, err := registration.NewSelfCancellationPolicy(
		"cancel-v3",
		3*time.Hour,
	)
	if err != nil {
		t.Fatalf("NewSelfCancellationPolicy() error = %v", err)
	}
	canceller, tx, capture := newCancellationHarness(
		current,
		cancellationScenario{},
		now,
		nil,
	)
	canceller.selfPolicy = policy

	_, err = canceller.Cancel(
		context.Background(),
		selfCancellationCommand(current),
	)
	if !errors.Is(err, ErrRegistrationCancellationNotAllowed) {
		t.Fatalf("Cancel(free after cutoff) error = %v", err)
	}
	if tx.committed || !tx.rolledBack ||
		capture.registrationUpdates != 0 ||
		capture.confirmedCapacityReleases != 0 {
		t.Fatalf("transaction/capture = %+v %+v", tx, capture)
	}
}

func TestRegistrationCancellerAllowsFreeParticipationAtConfiguredCutoff(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	current := confirmedRegistration(now.Add(-time.Hour))
	policy, err := registration.NewSelfCancellationPolicy(
		"cancel-v3",
		2*time.Hour,
	)
	if err != nil {
		t.Fatalf("NewSelfCancellationPolicy() error = %v", err)
	}
	canceller, tx, capture := newCancellationHarness(
		current,
		cancellationScenario{},
		now,
		nil,
	)
	canceller.selfPolicy = policy

	result, err := canceller.Cancel(
		context.Background(),
		selfCancellationCommand(current),
	)
	if err != nil {
		t.Fatalf("Cancel(free at cutoff) error = %v", err)
	}
	if result.PolicyVersion != "cancel-v3" ||
		result.Registration.CancellationReason == nil ||
		*result.Registration.CancellationReason != "user_cancelled@cancel-v3" ||
		!tx.committed || tx.rolledBack ||
		capture.registrationUpdates != 1 ||
		capture.confirmedCapacityReleases != 1 {
		t.Fatalf("result=%+v transaction=%+v capture=%+v", result, tx, capture)
	}
}

func TestRegistrationCancellerCancelsPaidParticipationWithPolicyAndRefund(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	current, order, hold := paidCancellationFixture(t, now.Add(-time.Hour), true)
	policy := &fakePaidCancellationPolicy{decision: PaidSelfCancellationPolicyDecision{
		Allowed:       true,
		FullRefund:    true,
		PolicyVersion: "cancel:v3",
	}}
	canceller, tx, capture := newCancellationHarness(
		current,
		cancellationScenario{order: &order, hold: &hold},
		now,
		policy,
	)
	result, err := canceller.Cancel(context.Background(), selfCancellationCommand(current))
	if err != nil {
		t.Fatalf("Cancel(paid) error = %v", err)
	}
	if result.Registration.CancellationReason == nil ||
		*result.Registration.CancellationReason != "user_cancelled@cancel:v3" ||
		result.PolicyVersion != "cancel:v3" ||
		result.Order == nil || result.Order.PaymentStatus != payment.OrderStatusPaidConfirmed ||
		result.Hold == nil || result.Hold.HoldStatus != payment.CapacityHoldStatusConverted ||
		result.Refund == nil ||
		result.Refund.RefundStatus != refund.StatusPendingManual ||
		result.Refund.ReasonCode != refund.ReasonUserCancelled ||
		result.Refund.RequestedRefundCents != order.PayableCents {
		t.Fatalf("Cancel(paid) = %+v", result)
	}
	if policy.calls != 1 || policy.input.OrderStatus != payment.OrderStatusPaidConfirmed ||
		policy.input.RegistrationID != current.ID || policy.input.EvaluatedAt != now {
		t.Fatalf("policy calls/input = %d %+v", policy.calls, policy.input)
	}
	if !tx.committed || tx.rolledBack ||
		capture.registrationUpdates != 1 ||
		capture.confirmedCapacityReleases != 1 ||
		capture.holdCapacityReleases != 0 ||
		capture.refundLookups != 1 ||
		capture.refundCreates != 1 {
		t.Fatalf("transaction/capture = %+v %+v", tx, capture)
	}
	if !reflect.DeepEqual(capture.lockOrder, []string{
		"series", "instance", "session", "registration", "order", "hold", "refund",
	}) {
		t.Fatalf("lock order = %v", capture.lockOrder)
	}
}

func TestRegistrationCancellerAppliesCouponPolicyForZeroSettlement(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	current, order, hold, ledger := zeroSettledCancellationFixture(
		t,
		now.Add(-time.Hour),
	)
	paidPolicy := &fakePaidCancellationPolicy{
		decision: PaidSelfCancellationPolicyDecision{
			Allowed:       true,
			FullRefund:    true,
			PolicyVersion: "cancel-v5",
		},
	}
	couponPolicy := &fakeRegistrationCouponPolicy{
		decision: coupon.RefundPolicyDecision{
			Configured:    true,
			PolicyVersion: "coupon-refund-v3",
			Disposition:   coupon.RefundDispositionRestore,
		},
	}
	canceller, tx, capture := newCancellationHarness(
		current,
		cancellationScenario{order: &order, hold: &hold, coupon: &ledger},
		now,
		paidPolicy,
	)
	canceller.couponPolicy = couponPolicy

	result, err := canceller.Cancel(
		context.Background(),
		selfCancellationCommand(current),
	)
	if err != nil {
		t.Fatalf("Cancel(settled zero) error = %v", err)
	}
	if result.Refund != nil || result.CouponAdjustment == nil ||
		result.CouponAdjustment.EntryType != coupon.EntryTypeRestored ||
		result.CouponAdjustment.RefundCaseID != nil ||
		result.PolicyVersion != "cancel-v5" ||
		paidPolicy.calls != 1 ||
		paidPolicy.input.OrderStatus != payment.OrderStatusSettledZero ||
		couponPolicy.calls != 1 ||
		couponPolicy.input.Trigger !=
			coupon.RefundTriggerSettledZeroCancellation ||
		len(capture.couponAppends) != 1 || !tx.committed || tx.rolledBack {
		t.Fatalf(
			"Cancel(settled zero) = %+v paid=%+v coupon=%+v tx=%+v capture=%+v",
			result,
			paidPolicy,
			couponPolicy,
			tx,
			capture,
		)
	}

	replayLedger := ledger
	replayLedger.Entries = append(
		replayLedger.Entries,
		*result.CouponAdjustment,
	)
	replayCanceller, replayTx, replayCapture := newCancellationHarness(
		result.Registration,
		cancellationScenario{
			order:  &order,
			hold:   &hold,
			coupon: &replayLedger,
		},
		now.Add(time.Minute),
		nil,
	)
	replayed, err := replayCanceller.Cancel(
		context.Background(),
		selfCancellationCommand(result.Registration),
	)
	if err != nil || replayed.CouponAdjustment == nil ||
		replayed.CouponAdjustment.ID != result.CouponAdjustment.ID ||
		len(replayCapture.couponAppends) != 0 ||
		!replayTx.committed || replayTx.rolledBack {
		t.Fatalf(
			"Cancel(settled zero replay) = %+v error=%v tx=%+v capture=%+v",
			replayed,
			err,
			replayTx,
			replayCapture,
		)
	}
}

func TestRegistrationCancellerFailsClosedWithoutZeroSettlementCouponPolicy(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	current, order, hold, ledger := zeroSettledCancellationFixture(
		t,
		now.Add(-time.Hour),
	)
	canceller, tx, capture := newCancellationHarness(
		current,
		cancellationScenario{order: &order, hold: &hold, coupon: &ledger},
		now,
		&fakePaidCancellationPolicy{
			decision: PaidSelfCancellationPolicyDecision{
				Allowed:       true,
				FullRefund:    true,
				PolicyVersion: "cancel-v5",
			},
		},
	)
	if _, err := canceller.Cancel(
		context.Background(),
		selfCancellationCommand(current),
	); !errors.Is(err, ErrRegistrationCouponPolicyMissing) {
		t.Fatalf("Cancel(missing Coupon policy) error = %v", err)
	}
	if tx.committed || !tx.rolledBack || len(capture.couponAppends) != 0 {
		t.Fatalf("transaction=%+v capture=%+v", tx, capture)
	}
}

func TestRegistrationCancellerClosesPendingOrderAndReleasesHold(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	current, order, hold := paidCancellationFixture(t, now.Add(-time.Minute), false)
	canceller, tx, capture := newCancellationHarness(
		current,
		cancellationScenario{order: &order, hold: &hold},
		now,
		nil,
	)
	result, err := canceller.Cancel(context.Background(), selfCancellationCommand(current))
	if err != nil {
		t.Fatalf("Cancel(pending) error = %v", err)
	}
	if result.Order == nil || result.Order.PaymentStatus != payment.OrderStatusClosedUnpaid ||
		result.Hold == nil || result.Hold.HoldStatus != payment.CapacityHoldStatusReleased ||
		result.Refund != nil || result.PolicyVersion != "" {
		t.Fatalf("Cancel(pending) = %+v", result)
	}
	if !tx.committed || tx.rolledBack ||
		capture.orderUpdates != 1 || capture.holdUpdates != 1 ||
		capture.registrationUpdates != 1 ||
		capture.holdCapacityReleases != 1 ||
		capture.couponReleases != 1 ||
		capture.confirmedCapacityReleases != 0 ||
		capture.refundCreates != 0 {
		t.Fatalf("transaction/capture = %+v %+v", tx, capture)
	}
}

func TestRegistrationCancellerRequiresPolicyForAmbiguousPayment(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	current, order, hold := paidCancellationFixture(t, now.Add(-time.Minute), false)
	unknownOrder, _, err := payment.MarkOrderUnknown(order, order.CreatedAt.Add(30*time.Second))
	if err != nil {
		t.Fatalf("MarkOrderUnknown() error = %v", err)
	}

	for _, test := range []struct {
		name    string
		policy  PaidSelfCancellationPolicy
		wantErr error
	}{
		{name: "missing", wantErr: ErrRegistrationCancellationPolicyMissing},
		{
			name: "denied",
			policy: &fakePaidCancellationPolicy{decision: PaidSelfCancellationPolicyDecision{
				Allowed:       false,
				PolicyVersion: "cancel-v3",
			}},
			wantErr: ErrRegistrationCancellationNotAllowed,
		},
		{
			name: "not full refund",
			policy: &fakePaidCancellationPolicy{decision: PaidSelfCancellationPolicyDecision{
				Allowed:       true,
				FullRefund:    false,
				PolicyVersion: "cancel-v3",
			}},
			wantErr: ErrRegistrationCancellationPolicyMissing,
		},
		{
			name: "invalid version",
			policy: &fakePaidCancellationPolicy{decision: PaidSelfCancellationPolicyDecision{
				Allowed:       true,
				FullRefund:    true,
				PolicyVersion: "bad version",
			}},
			wantErr: ErrRegistrationCancellationPolicyMissing,
		},
		{
			name:    "evaluator failure",
			policy:  &fakePaidCancellationPolicy{err: errors.New("configuration read failed")},
			wantErr: ErrRegistrationCancellationPolicyMissing,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			canceller, tx, capture := newCancellationHarness(
				current,
				cancellationScenario{order: &unknownOrder, hold: &hold},
				now,
				test.policy,
			)
			if _, err := canceller.Cancel(
				context.Background(),
				selfCancellationCommand(current),
			); !errors.Is(err, test.wantErr) {
				t.Fatalf("Cancel(unknown) error = %v, want %v", err, test.wantErr)
			}
			if tx.committed || !tx.rolledBack ||
				capture.registrationUpdates != 0 || capture.holdUpdates != 0 ||
				capture.holdCapacityReleases != 0 {
				t.Fatalf("transaction/capture = %+v %+v", tx, capture)
			}
		})
	}
}

func TestRegistrationCancellerPreservesUnknownOrderAfterAllowedCancellation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	current, order, hold := paidCancellationFixture(t, now.Add(-time.Minute), false)
	unknownOrder, _, err := payment.MarkOrderUnknown(order, order.CreatedAt.Add(30*time.Second))
	if err != nil {
		t.Fatalf("MarkOrderUnknown() error = %v", err)
	}
	policy := &fakePaidCancellationPolicy{decision: PaidSelfCancellationPolicyDecision{
		Allowed:       true,
		FullRefund:    true,
		PolicyVersion: "cancel-v4",
	}}
	canceller, tx, capture := newCancellationHarness(
		current,
		cancellationScenario{order: &unknownOrder, hold: &hold},
		now,
		policy,
	)
	result, err := canceller.Cancel(context.Background(), selfCancellationCommand(current))
	if err != nil {
		t.Fatalf("Cancel(unknown allowed) error = %v", err)
	}
	if result.Order == nil || result.Order.PaymentStatus != payment.OrderStatusUnknown ||
		result.Hold == nil || result.Hold.HoldStatus != payment.CapacityHoldStatusReleased ||
		result.Refund != nil || result.PolicyVersion != "cancel-v4" ||
		result.Registration.CancellationReason == nil ||
		*result.Registration.CancellationReason != "user_cancelled@cancel-v4" {
		t.Fatalf("Cancel(unknown allowed) = %+v", result)
	}
	if !tx.committed || tx.rolledBack || capture.orderUpdates != 0 ||
		capture.holdUpdates != 1 || capture.holdCapacityReleases != 1 {
		t.Fatalf("transaction/capture = %+v %+v", tx, capture)
	}
}

func TestRegistrationCancellerOperatorBypassesSelfPolicy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	current, order, hold := paidCancellationFixture(t, now.Add(-time.Hour), true)
	operatorID := uuid.New()
	command := CancelRegistrationCommand{
		TenantID:       current.TenantID,
		RegistrationID: current.ID,
		ActorID:        operatorID,
		Source:         CancellationSourceOperator,
		Reason:         "customer support approved exception",
	}
	canceller, tx, capture := newCancellationHarness(
		current,
		cancellationScenario{order: &order, hold: &hold},
		now,
		&fakePaidCancellationPolicy{err: errors.New("must not be called")},
	)
	result, err := canceller.Cancel(context.Background(), command)
	if err != nil {
		t.Fatalf("Cancel(operator) error = %v", err)
	}
	wantReason := "operator_cancelled:" + operatorID.String() + ":" + command.Reason
	if result.Registration.CancellationReason == nil ||
		*result.Registration.CancellationReason != wantReason ||
		result.Refund == nil || result.Refund.ReasonCode != refund.ReasonOperatorAdjustment ||
		result.Refund.OperatorNote == nil ||
		!strings.Contains(*result.Refund.OperatorNote, operatorID.String()) ||
		capture.refundCreates != 1 || !tx.committed {
		t.Fatalf("Cancel(operator) = %+v transaction=%+v capture=%+v", result, tx, capture)
	}
}

func TestRegistrationCancellerReplaysRecordedPaidCancellationWithoutPolicy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	current, order, hold := paidCancellationFixture(t, now.Add(-time.Hour), true)
	cancelled, _, err := registration.CancelRegistration(
		current,
		"user_cancelled@cancel-v3",
		now.Add(-time.Minute),
	)
	if err != nil {
		t.Fatalf("CancelRegistration() error = %v", err)
	}
	existingRefund, err := refund.NewCase(refund.NewCaseCommand{
		TenantID:             order.TenantID,
		OrderID:              order.ID,
		RegistrationID:       order.RegistrationID,
		SeriesID:             order.SeriesID,
		InstanceID:           order.InstanceID,
		SessionID:            order.SessionID,
		PrincipalID:          order.PrincipalID,
		ReasonCode:           refund.ReasonUserCancelled,
		IdempotencyKey:       "refund:cancellation:" + current.ID.String(),
		ActualPaidCents:      *order.ActualPaidCents,
		RequestedRefundCents: *order.ActualPaidCents,
		Now:                  now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("NewCase() error = %v", err)
	}
	canceller, tx, capture := newCancellationHarness(
		cancelled,
		cancellationScenario{order: &order, hold: &hold, refundCase: &existingRefund},
		now,
		nil,
	)
	result, err := canceller.Cancel(context.Background(), selfCancellationCommand(cancelled))
	if err != nil {
		t.Fatalf("Cancel(replay) error = %v", err)
	}
	if result.Refund == nil || result.Refund.ID != existingRefund.ID ||
		result.PolicyVersion != "cancel-v3" || !tx.committed || tx.rolledBack ||
		capture.registrationUpdates != 0 || capture.confirmedCapacityReleases != 0 ||
		capture.refundCreates != 0 || capture.refundLookups != 1 {
		t.Fatalf("Cancel(replay) = %+v transaction=%+v capture=%+v", result, tx, capture)
	}
}

func TestRegistrationCancellerRejectsInvalidOwnershipAndConflictingReplay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	current := confirmedRegistration(now.Add(-time.Hour))
	forbidden := selfCancellationCommand(current)
	forbidden.ActorID = uuid.New()
	canceller, tx, _ := newCancellationHarness(current, cancellationScenario{}, now, nil)
	if _, err := canceller.Cancel(context.Background(), forbidden); !errors.Is(
		err,
		ErrRegistrationCancellationForbidden,
	) {
		t.Fatalf("Cancel(forbidden) error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("forbidden transaction = %+v", tx)
	}

	cancelled, _, err := registration.CancelRegistration(
		current,
		"operator_cancelled:"+uuid.New().String()+":other reason",
		now.Add(-time.Minute),
	)
	if err != nil {
		t.Fatalf("CancelRegistration() error = %v", err)
	}
	canceller, tx, _ = newCancellationHarness(cancelled, cancellationScenario{}, now, nil)
	if _, err := canceller.Cancel(
		context.Background(),
		selfCancellationCommand(cancelled),
	); !errors.Is(err, ErrRegistrationCancellationConflict) {
		t.Fatalf("Cancel(conflicting replay) error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("conflicting replay transaction = %+v", tx)
	}
}

func TestRegistrationCancellerValidatesBeforeLookupAndTranslatesMissing(t *testing.T) {
	t.Parallel()

	resolver := &fakeCancellationResolver{}
	starter := &fakeCancellationTransactionStarter{}
	canceller := &RegistrationCanceller{
		registrations: resolver,
		transactions:  starter,
		now:           time.Now,
	}
	if _, err := canceller.Cancel(context.Background(), CancelRegistrationCommand{}); !errors.Is(
		err,
		ErrInvalidRegistrationCancellationCommand,
	) {
		t.Fatalf("Cancel(invalid) error = %v", err)
	}
	if resolver.gets != 0 || starter.begins != 0 {
		t.Fatalf("invalid resolver/starter = %+v %+v", resolver, starter)
	}

	command := CancelRegistrationCommand{
		TenantID:       uuid.New(),
		RegistrationID: uuid.New(),
		ActorID:        uuid.New(),
		Source:         CancellationSourceSelf,
	}
	resolver.err = ErrRegistrationNotFound
	if _, err := canceller.Cancel(context.Background(), command); !errors.Is(
		err,
		ErrRegistrationCancellationNotFound,
	) {
		t.Fatalf("Cancel(missing) error = %v", err)
	}
	if starter.begins != 0 {
		t.Fatalf("missing target began %d transaction(s)", starter.begins)
	}
}

func TestRegistrationCancellationErrorsAreClassified(t *testing.T) {
	t.Parallel()

	if got := classifyCancellationWriteError(ErrRegistrationVersionConflict); !errors.Is(
		got,
		ErrRegistrationCancellationTransaction,
	) {
		t.Fatalf("classify version = %v", got)
	}
	if got := classifyCancellationWriteError(&pgconn.PgError{Code: "23505"}); !errors.Is(
		got,
		ErrRegistrationCancellationConflict,
	) {
		t.Fatalf("classify unique = %v", got)
	}
	if got := classifyCancellationCommitError(&pgconn.PgError{Code: "40001"}); !errors.Is(
		got,
		ErrRegistrationCancellationTransaction,
	) {
		t.Fatalf("classify serialization = %v", got)
	}
}

type cancellationScenario struct {
	order      *payment.Order
	hold       *payment.CapacityHold
	refundCase *refund.Case
	coupon     *couponpostgres.Ledger
	commitErr  error
}

type cancellationCapture struct {
	isolation                 sql.IsolationLevel
	lockOrder                 []string
	registrationUpdates       int
	orderUpdates              int
	holdUpdates               int
	confirmedCapacityReleases int
	holdCapacityReleases      int
	refundLookups             int
	refundCreates             int
	couponReleases            int
	couponAppends             []coupon.Entry
}

func newCancellationHarness(
	current registration.Registration,
	scenario cancellationScenario,
	now time.Time,
	policy PaidSelfCancellationPolicy,
) (*RegistrationCanceller, *fakeCancellationTransaction, *cancellationCapture) {
	capture := &cancellationCapture{}
	tx := &fakeCancellationTransaction{
		current:  current,
		scenario: scenario,
		capture:  capture,
	}
	starter := &fakeCancellationTransactionStarter{tx: tx, capture: capture}
	return &RegistrationCanceller{
		registrations: &fakeCancellationResolver{value: current},
		transactions:  starter,
		policy:        policy,
		now:           func() time.Time { return now },
	}, tx, capture
}

type fakeCancellationResolver struct {
	value registration.Registration
	err   error
	gets  int
}

func (resolver *fakeCancellationResolver) Get(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (registration.Registration, error) {
	resolver.gets++
	return resolver.value, resolver.err
}

type fakeCancellationTransactionStarter struct {
	tx      *fakeCancellationTransaction
	capture *cancellationCapture
	err     error
	begins  int
}

func (starter *fakeCancellationTransactionStarter) beginCancellationTx(
	_ context.Context,
	options *sql.TxOptions,
) (cancellationTransaction, error) {
	starter.begins++
	if starter.capture != nil {
		starter.capture.isolation = options.Isolation
	}
	return starter.tx, starter.err
}

type fakeCancellationTransaction struct {
	current    registration.Registration
	scenario   cancellationScenario
	capture    *cancellationCapture
	committed  bool
	rolledBack bool
}

func (tx *fakeCancellationTransaction) lockSeries(context.Context, uuid.UUID, uuid.UUID) error {
	tx.capture.lockOrder = append(tx.capture.lockOrder, "series")
	return nil
}

func (tx *fakeCancellationTransaction) lockInstance(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) error {
	tx.capture.lockOrder = append(tx.capture.lockOrder, "instance")
	return nil
}

func (tx *fakeCancellationTransaction) lockSession(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (lockedCancellationSession, error) {
	tx.capture.lockOrder = append(tx.capture.lockOrder, "session")
	return lockedCancellationSession{
		sessionStartAt: tx.current.CreatedAt.Add(3 * time.Hour),
		version:        9,
	}, nil
}

func (tx *fakeCancellationTransaction) lockRegistration(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (registration.Registration, error) {
	tx.capture.lockOrder = append(tx.capture.lockOrder, "registration")
	return tx.current, nil
}

func (tx *fakeCancellationTransaction) lockOrderByRegistration(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (payment.Order, error) {
	tx.capture.lockOrder = append(tx.capture.lockOrder, "order")
	if tx.scenario.order == nil {
		return payment.Order{}, errCancellationOrderNotFound
	}
	return *tx.scenario.order, nil
}

func (tx *fakeCancellationTransaction) lockHoldByOrder(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (payment.CapacityHold, error) {
	tx.capture.lockOrder = append(tx.capture.lockOrder, "hold")
	if tx.scenario.hold == nil {
		return payment.CapacityHold{}, ErrRegistrationCancellationTransaction
	}
	return *tx.scenario.hold, nil
}

func (tx *fakeCancellationTransaction) lockRefundByOrder(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (refund.Case, error) {
	tx.capture.lockOrder = append(tx.capture.lockOrder, "refund")
	tx.capture.refundLookups++
	if tx.scenario.refundCase == nil {
		return refund.Case{}, errCancellationRefundNotFound
	}
	return *tx.scenario.refundCase, nil
}

func (tx *fakeCancellationTransaction) lockCouponByOrder(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (couponpostgres.Ledger, error) {
	tx.capture.lockOrder = append(tx.capture.lockOrder, "coupon")
	if tx.scenario.coupon == nil {
		return couponpostgres.Ledger{}, ErrRegistrationCancellationTransaction
	}
	return *tx.scenario.coupon, nil
}

func (tx *fakeCancellationTransaction) updateRegistration(
	_ context.Context,
	value registration.Registration,
	_ int64,
) (registration.Registration, error) {
	tx.capture.registrationUpdates++
	return value, nil
}

func (tx *fakeCancellationTransaction) updateOrder(
	_ context.Context,
	value payment.Order,
	_ int64,
) (payment.Order, error) {
	tx.capture.orderUpdates++
	return value, nil
}

func (tx *fakeCancellationTransaction) updateHold(
	_ context.Context,
	value payment.CapacityHold,
	_ int64,
) (payment.CapacityHold, error) {
	tx.capture.holdUpdates++
	return value, nil
}

func (tx *fakeCancellationTransaction) releaseCouponForOrder(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	string,
	time.Time,
) error {
	tx.capture.couponReleases++
	return nil
}

func (tx *fakeCancellationTransaction) appendCouponEntry(
	_ context.Context,
	value coupon.Entry,
) (coupon.Entry, error) {
	tx.capture.couponAppends = append(tx.capture.couponAppends, value)
	return value, nil
}

func (tx *fakeCancellationTransaction) createRefund(
	_ context.Context,
	value refund.Case,
) (refund.Case, error) {
	tx.capture.refundCreates++
	return value, nil
}

func (tx *fakeCancellationTransaction) decrementConfirmedCapacity(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
	int64,
	time.Time,
) error {
	tx.capture.confirmedCapacityReleases++
	return nil
}

func (tx *fakeCancellationTransaction) decrementHoldCapacity(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
	int64,
	time.Time,
) error {
	tx.capture.holdCapacityReleases++
	return nil
}

func (tx *fakeCancellationTransaction) Commit() error {
	if tx.scenario.commitErr != nil {
		return tx.scenario.commitErr
	}
	tx.committed = true
	return nil
}

func (tx *fakeCancellationTransaction) Rollback() error {
	tx.rolledBack = true
	return nil
}

type fakePaidCancellationPolicy struct {
	decision PaidSelfCancellationPolicyDecision
	input    PaidSelfCancellationPolicyInput
	err      error
	calls    int
}

type fakeRegistrationCouponPolicy struct {
	decision coupon.RefundPolicyDecision
	input    coupon.RefundPolicyInput
	err      error
	calls    int
}

func (policy *fakeRegistrationCouponPolicy) EvaluateCouponRefund(
	_ context.Context,
	input coupon.RefundPolicyInput,
) (coupon.RefundPolicyDecision, error) {
	policy.calls++
	policy.input = input
	return policy.decision, policy.err
}

func (policy *fakePaidCancellationPolicy) EvaluatePaidSelfCancellation(
	_ context.Context,
	input PaidSelfCancellationPolicyInput,
) (PaidSelfCancellationPolicyDecision, error) {
	policy.calls++
	policy.input = input
	return policy.decision, policy.err
}

func selfCancellationCommand(current registration.Registration) CancelRegistrationCommand {
	return CancelRegistrationCommand{
		TenantID:       current.TenantID,
		RegistrationID: current.ID,
		ActorID:        current.PrincipalID,
		Source:         CancellationSourceSelf,
	}
}

func paidCancellationFixture(
	t *testing.T,
	createdAt time.Time,
	confirmed bool,
) (registration.Registration, payment.Order, payment.CapacityHold) {
	t.Helper()
	current, err := registration.NewRegistration(registration.NewRegistrationCommand{
		TenantID:        uuid.New(),
		SeriesID:        uuid.New(),
		InstanceID:      uuid.New(),
		SessionID:       uuid.New(),
		PrincipalID:     uuid.New(),
		IdempotencyKey:  "registration:cancellation:test",
		RequiresPayment: true,
		Now:             createdAt,
	})
	if err != nil {
		t.Fatalf("NewRegistration() error = %v", err)
	}
	contextValue, err := payment.NewPaymentContext(payment.NewPaymentContextCommand{
		TenantID:           current.TenantID,
		RegistrationID:     current.ID,
		SeriesID:           current.SeriesID,
		InstanceID:         current.InstanceID,
		SessionID:          current.SessionID,
		PrincipalID:        current.PrincipalID,
		IdempotencyKey:     "registration:cancellation:test",
		MerchantOrderNo:    "merchant-order-cancellation-test",
		PaymentAppID:       "wx-app-cancellation-test",
		PaymentMerchantID:  "wx-merchant-cancellation-test",
		OriginalPriceCents: 10_000,
		DiscountCents:      1_000,
		Now:                createdAt,
	})
	if err != nil {
		t.Fatalf("NewPaymentContext() error = %v", err)
	}
	if !confirmed {
		return current, contextValue.Order, contextValue.Hold
	}
	confirmedAt := createdAt.Add(time.Minute)
	current, _, err = registration.ConfirmRegistration(current, confirmedAt)
	if err != nil {
		t.Fatalf("ConfirmRegistration() error = %v", err)
	}
	contextValue.Order, _, err = payment.ConfirmOrderPayment(
		contextValue.Order,
		payment.PaymentConfirmation{
			ActualPaidCents:     contextValue.Order.PayableCents,
			WeChatTransactionID: "wx-transaction-cancellation-test",
			PaidAt:              confirmedAt,
		},
	)
	if err != nil {
		t.Fatalf("ConfirmOrderPayment() error = %v", err)
	}
	contextValue.Hold, _, err = payment.ConvertCapacityHold(contextValue.Hold, confirmedAt)
	if err != nil {
		t.Fatalf("ConvertCapacityHold() error = %v", err)
	}
	return current, contextValue.Order, contextValue.Hold
}

func zeroSettledCancellationFixture(
	t *testing.T,
	createdAt time.Time,
) (
	registration.Registration,
	payment.Order,
	payment.CapacityHold,
	couponpostgres.Ledger,
) {
	t.Helper()
	current, order, hold := paidCancellationFixture(t, createdAt, false)
	order.DiscountCents = order.OriginalPriceCents
	order.PayableCents = 0
	activityType := activity.ActivityTypeAIRoundtable
	grant, err := coupon.NewManualReplenishment(
		coupon.ManualReplenishmentCommand{
			TenantID:    current.TenantID,
			PrincipalID: current.PrincipalID,
			ActorID:     uuid.New(),
			BusinessKey: "registration-cancel-zero",
			Reason:      "test zero-settlement cancellation",
			Context:     "Registration cancellation",
			Policy: coupon.GrantPolicy{
				Configured:        true,
				PolicyVersion:     "grant-v1",
				FaceValueCents:    order.DiscountCents,
				Validity:          24 * time.Hour,
				ScopeType:         coupon.ScopeTypeActivityType,
				ScopeActivityType: &activityType,
			},
			GrantedAt:  createdAt.Add(-time.Minute),
			RecordedAt: createdAt.Add(-time.Minute),
		},
	)
	if err != nil {
		t.Fatalf("NewManualReplenishment() error = %v", err)
	}
	instrument := grant.Coupons[0]
	history := []coupon.Entry{grant.Entries[0]}
	held, err := coupon.Hold(coupon.HoldCommand{
		Instrument:         instrument,
		History:            history,
		OrderID:            order.ID,
		RegistrationID:     current.ID,
		SeriesID:           current.SeriesID,
		ActivityType:       activityType,
		OriginalPriceCents: order.OriginalPriceCents,
		HoldExpiresAt:      hold.ExpiresAt,
		At:                 createdAt,
		RecordedAt:         createdAt,
	})
	if err != nil {
		t.Fatalf("Hold(Coupon) error = %v", err)
	}
	history = append(history, held)
	settledAt := createdAt.Add(time.Minute)
	order, _, err = payment.SettleOrderZero(order, settledAt)
	if err != nil {
		t.Fatalf("SettleOrderZero() error = %v", err)
	}
	current, _, err = registration.ConfirmRegistration(current, settledAt)
	if err != nil {
		t.Fatalf("ConfirmRegistration() error = %v", err)
	}
	hold, _, err = payment.ConvertCapacityHold(hold, settledAt)
	if err != nil {
		t.Fatalf("ConvertCapacityHold() error = %v", err)
	}
	redeemed, err := coupon.Redeem(coupon.RedeemCommand{
		Instrument: instrument,
		History:    history,
		OrderID:    order.ID,
		At:         settledAt,
		RecordedAt: settledAt,
	})
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}
	return current, order, hold, couponpostgres.Ledger{
		Instrument: instrument,
		Entries:    append(history, redeemed),
	}
}
