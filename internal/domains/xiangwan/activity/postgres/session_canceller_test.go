package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
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

func TestSessionCancellerConvergesParticipationPaymentAndRefund(t *testing.T) {
	t.Parallel()

	fixture := newSessionCancellationFixture(t)
	canceller, tx, starter := newSessionCancellerHarness(fixture)

	result, err := canceller.Cancel(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if starter.isolation != sql.LevelSerializable {
		t.Fatalf("transaction isolation = %v", starter.isolation)
	}
	if got, want := tx.lockOrder[:3], []string{"series", "instance", "session"}; !sameStrings(got, want) {
		t.Fatalf("hierarchy lock order = %v, want %v", got, want)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}
	if result.Session.Status != activity.SessionStatusCancelled ||
		result.Session.ConfirmedRegistrationCount != 0 ||
		result.Session.ActiveHoldCount != 0 ||
		result.Session.Version != fixture.session.Version+1 {
		t.Fatalf("cancelled Session = %+v", result.Session)
	}
	if result.Receipt.CancelledRegistrationCount != 3 ||
		result.Receipt.ReleasedConfirmedCount != 2 ||
		result.Receipt.ReleasedHoldCount != 1 ||
		result.Receipt.ClosedPendingOrderCount != 1 ||
		result.Receipt.RefundCaseCount != 1 ||
		result.Receipt.RequestedRefundCents != 9_000 {
		t.Fatalf("cancellation receipt = %+v", result.Receipt)
	}
	if len(tx.registrationUpdates) != 3 {
		t.Fatalf("Registration updates = %d", len(tx.registrationUpdates))
	}
	for _, updated := range tx.registrationUpdates {
		if updated.ParticipationStatus != registration.ParticipationStatusCancelled ||
			updated.CancellationReason == nil ||
			*updated.CancellationReason != "session_cancelled" {
			t.Fatalf("cancelled Registration = %+v", updated)
		}
	}
	if len(tx.orderUpdates) != 1 ||
		tx.orderUpdates[0].PaymentStatus != payment.OrderStatusClosedUnpaid {
		t.Fatalf("Order updates = %+v", tx.orderUpdates)
	}
	if len(tx.holdUpdates) != 1 ||
		tx.holdUpdates[0].HoldStatus != payment.CapacityHoldStatusReleased ||
		tx.holdUpdates[0].ReleaseReason == nil ||
		*tx.holdUpdates[0].ReleaseReason != "session_cancelled" {
		t.Fatalf("hold updates = %+v", tx.holdUpdates)
	}
	if len(tx.refundCreates) != 1 {
		t.Fatalf("Refund creates = %d", len(tx.refundCreates))
	}
	createdRefund := tx.refundCreates[0]
	if createdRefund.ReasonCode != refund.ReasonSessionCancelled ||
		createdRefund.RequestedRefundCents != 9_000 ||
		createdRefund.OrderID != fixture.paidOrder.ID ||
		createdRefund.RegistrationID != fixture.paidRegistration.ID {
		t.Fatalf("created Refund = %+v", createdRefund)
	}
	if tx.receiptCreated.ID != result.Receipt.ID ||
		tx.sessionUpdated.ID != result.Session.ID ||
		result.Receipt.PreviewID != fixture.preview.ID ||
		result.Receipt.NotificationStrategy != fixture.preview.NotificationStrategy ||
		tx.previewConsumed.ConsumedAt == nil {
		t.Fatalf(
			"stored cancellation session=%+v receipt=%+v",
			tx.sessionUpdated,
			tx.receiptCreated,
		)
	}
}

func TestSessionCancellerAppliesCouponPolicyForZeroSettlement(t *testing.T) {
	t.Parallel()

	fixture := newSessionCancellationFixture(t)
	addZeroSettledSessionRegistration(t, &fixture)
	canceller, tx, _ := newSessionCancellerHarness(fixture)
	policy := &fakeActivityCouponPolicy{
		decision: coupon.RefundPolicyDecision{
			Configured:    true,
			PolicyVersion: "coupon-cancel-v1",
			Disposition:   coupon.RefundDispositionRestore,
		},
	}
	canceller.couponPolicy = policy

	result, err := canceller.Cancel(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf("Cancel(zero settlement) error = %v", err)
	}
	if fixture.preview.CouponAdjustmentCount != 1 ||
		result.Receipt.CouponAdjustmentCount != 1 ||
		len(tx.couponAppends) != 1 ||
		tx.couponAppends[0].EntryType != coupon.EntryTypeRestored ||
		tx.couponAppends[0].RefundCaseID != nil || policy.calls != 1 ||
		policy.input.Trigger != coupon.RefundTriggerSettledZeroCancellation ||
		result.Receipt.RefundCaseCount != 1 || len(tx.refundCreates) != 1 ||
		!tx.committed || tx.rolledBack {
		t.Fatalf(
			"Cancel(zero settlement)=%+v policy=%+v tx=%+v",
			result,
			policy,
			tx,
		)
	}
}

func TestSessionCancellerLeavesUnknownOrderForProviderResolution(t *testing.T) {
	t.Parallel()

	fixture := newSessionCancellationFixture(t)
	pendingOrder := fixture.orders[fixture.pendingRegistration.ID]
	pendingOrder.PaymentStatus = payment.OrderStatusUnknown
	fixture.orders[fixture.pendingRegistration.ID] = pendingOrder
	refreshSessionCancellationFixturePreview(t, &fixture)
	canceller, tx, _ := newSessionCancellerHarness(fixture)

	result, err := canceller.Cancel(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if result.Receipt.ClosedPendingOrderCount != 0 ||
		result.Receipt.ReleasedHoldCount != 1 ||
		len(tx.orderUpdates) != 0 ||
		len(tx.holdUpdates) != 1 {
		t.Fatalf(
			"unknown order convergence result=%+v orders=%+v holds=%+v",
			result,
			tx.orderUpdates,
			tx.holdUpdates,
		)
	}
}

func TestSessionCancellerExactReplayReturnsReceipt(t *testing.T) {
	t.Parallel()

	fixture := newSessionCancellationFixture(t)
	receipt := activity.SessionCancellationReceipt{
		ID:                         uuid.New(),
		TenantID:                   fixture.command.TenantID,
		SeriesID:                   fixture.target.SeriesID,
		InstanceID:                 fixture.target.InstanceID,
		SessionID:                  fixture.command.SessionID,
		PreviewID:                  fixture.command.PreviewID,
		IdempotencyKey:             fixture.command.IdempotencyKey,
		CancelledBy:                fixture.command.ActorID,
		CancellationReason:         fixture.command.Reason,
		NotificationStrategy:       fixture.command.NotificationStrategy,
		CancelledRegistrationCount: 3,
		ReleasedConfirmedCount:     2,
		ReleasedHoldCount:          1,
		ClosedPendingOrderCount:    1,
		RefundCaseCount:            1,
		RequestedRefundCents:       9_000,
		CancelledAt:                fixture.processedAt,
		ResultingSessionVersion:    fixture.session.Version + 1,
		CreatedAt:                  fixture.processedAt,
	}
	fixture.session.Status = activity.SessionStatusCancelled
	fixture.session.ConfirmedRegistrationCount = 0
	fixture.session.ActiveHoldCount = 0
	fixture.session.Version++
	fixture.session.UpdatedAt = fixture.processedAt
	fixture.receipt = receipt
	fixture.receiptErr = nil
	fixture.seriesStatus = activity.SeriesStatusArchived
	fixture.instanceStatus = activity.InstanceStatusCancelled
	canceller, tx, _ := newSessionCancellerHarness(fixture)

	result, err := canceller.Cancel(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf("Cancel(replay) error = %v", err)
	}
	if result.Receipt.ID != receipt.ID ||
		result.Session.ID != fixture.session.ID ||
		!tx.committed ||
		len(tx.registrationUpdates) != 0 ||
		tx.listRegistrationsCalled {
		t.Fatalf("Cancel(replay) result=%+v tx=%+v", result, tx)
	}
}

func TestSessionCancellerRejectsConflictingReplay(t *testing.T) {
	t.Parallel()

	fixture := newSessionCancellationFixture(t)
	fixture.session.Status = activity.SessionStatusCancelled
	fixture.session.ConfirmedRegistrationCount = 0
	fixture.session.ActiveHoldCount = 0
	fixture.receipt = activity.SessionCancellationReceipt{
		TenantID:           fixture.command.TenantID,
		SessionID:          fixture.command.SessionID,
		IdempotencyKey:     fixture.command.IdempotencyKey,
		CancelledBy:        fixture.command.ActorID,
		CancellationReason: "Different reason",
	}
	fixture.receiptErr = nil
	canceller, tx, _ := newSessionCancellerHarness(fixture)

	_, err := canceller.Cancel(context.Background(), fixture.command)
	if !errors.Is(err, ErrSessionCancellationConflict) {
		t.Fatalf("Cancel(conflict) error = %v", err)
	}
	if tx.committed || !tx.rolledBack || tx.listRegistrationsCalled {
		t.Fatalf("conflicting replay transaction = %+v", tx)
	}
}

func TestSessionCancellerRejectsInvalidOrUnavailableTarget(t *testing.T) {
	t.Parallel()

	fixture := newSessionCancellationFixture(t)
	invalid := fixture
	invalid.command.ActorID = uuid.Nil
	canceller, _, starter := newSessionCancellerHarness(invalid)
	if _, err := canceller.Cancel(
		context.Background(),
		invalid.command,
	); !errors.Is(err, ErrInvalidSessionCancellationCommand) {
		t.Fatalf("Cancel(invalid) error = %v", err)
	}
	if starter.started {
		t.Fatal("invalid command started a transaction")
	}

	missing := fixture
	missing.resolveErr = errSessionCancellationTargetNotFound
	canceller, _, starter = newSessionCancellerHarness(missing)
	if _, err := canceller.Cancel(
		context.Background(),
		missing.command,
	); !errors.Is(err, ErrSessionCancellationNotFound) {
		t.Fatalf("Cancel(missing) error = %v", err)
	}
	if starter.started {
		t.Fatal("missing target started a transaction")
	}

	unavailable := fixture
	unavailable.session.Status = activity.SessionStatusEnded
	canceller, tx, _ := newSessionCancellerHarness(unavailable)
	if _, err := canceller.Cancel(
		context.Background(),
		unavailable.command,
	); !errors.Is(err, ErrSessionCancellationConflict) {
		t.Fatalf("Cancel(ended) error = %v", err)
	}
	if tx.committed || !tx.rolledBack || tx.listRegistrationsCalled {
		t.Fatalf("unavailable transaction = %+v", tx)
	}
}

func TestSessionCancellerFailsClosedWhenCountersDoNotConverge(t *testing.T) {
	t.Parallel()

	fixture := newSessionCancellationFixture(t)
	fixture.session.ActiveHoldCount++
	canceller, tx, _ := newSessionCancellerHarness(fixture)

	_, err := canceller.Cancel(context.Background(), fixture.command)
	if !errors.Is(err, ErrSessionCancellationTransaction) {
		t.Fatalf("Cancel(unconverged) error = %v", err)
	}
	if tx.committed || !tx.rolledBack || tx.receiptCreated.ID != uuid.Nil {
		t.Fatalf("unconverged transaction = %+v", tx)
	}
}

func TestSessionCancellationErrorClassification(t *testing.T) {
	t.Parallel()

	unique := &pgconn.PgError{Code: "23505"}
	if err := classifySessionCancellationWriteError(unique); !errors.Is(
		err,
		ErrSessionCancellationConflict,
	) {
		t.Fatalf("unique write error = %v", err)
	}
	serialization := &pgconn.PgError{Code: "40001"}
	if err := classifySessionCancellationWriteError(serialization); !errors.Is(
		err,
		ErrSessionCancellationTransaction,
	) {
		t.Fatalf("serialization write error = %v", err)
	}
	if err := classifySessionCancellationCommitError(serialization); !errors.Is(
		err,
		ErrSessionCancellationTransaction,
	) {
		t.Fatalf("serialization commit error = %v", err)
	}
	plain := errors.New("plain database failure")
	if got := classifySessionCancellationWriteError(plain); got != plain {
		t.Fatalf("plain write error = %v", got)
	}
	if err := classifySessionCancellationCommitError(plain); !errors.Is(err, plain) {
		t.Fatalf("plain commit error = %v", err)
	}
	if err := classifySessionCancellationPreviewWriteError(unique); !errors.Is(
		err,
		ErrSessionCancellationPreviewConflict,
	) {
		t.Fatalf("preview unique write error = %v", err)
	}
}

type sessionCancellationFixture struct {
	command             SessionCancellationCommand
	target              sessionCancellationTarget
	session             activity.Session
	seriesStatus        activity.SeriesStatus
	instanceStatus      activity.InstanceStatus
	registrations       []registration.Registration
	freeRegistration    registration.Registration
	paidRegistration    registration.Registration
	pendingRegistration registration.Registration
	paidOrder           payment.Order
	orders              map[uuid.UUID]payment.Order
	holds               map[uuid.UUID]payment.CapacityHold
	refunds             map[uuid.UUID]refund.Case
	coupons             map[uuid.UUID]couponpostgres.Ledger
	preview             activity.SessionCancellationPreview
	receipt             activity.SessionCancellationReceipt
	receiptErr          error
	processedAt         time.Time
	resolveErr          error
}

func newSessionCancellationFixture(t *testing.T) sessionCancellationFixture {
	t.Helper()
	now := time.Date(2026, time.September, 13, 3, 0, 0, 0, time.UTC)
	tenantID := uuid.New()
	seriesID := uuid.New()
	instanceID := uuid.New()
	sessionID := uuid.New()
	newRegistration := func(key string, paid bool) registration.Registration {
		value, err := registration.NewRegistration(registration.NewRegistrationCommand{
			TenantID:        tenantID,
			SeriesID:        seriesID,
			InstanceID:      instanceID,
			SessionID:       sessionID,
			PrincipalID:     uuid.New(),
			IdempotencyKey:  key,
			RequiresPayment: paid,
			Now:             now.Add(-time.Minute),
		})
		if err != nil {
			t.Fatalf("NewRegistration() error = %v", err)
		}
		return value
	}
	freeRegistration := newRegistration("registration:cancel:free", false)
	paidRegistration := newRegistration("registration:cancel:paid", true)
	paidRegistration, changed, err := registration.ConfirmRegistration(
		paidRegistration,
		now.Add(-30*time.Second),
	)
	if err != nil || !changed {
		t.Fatalf("ConfirmRegistration() changed=%t error=%v", changed, err)
	}
	pendingRegistration := newRegistration("registration:cancel:pending", true)

	newPayment := func(value registration.Registration, key string) payment.PaymentContext {
		created, err := payment.NewPaymentContext(payment.NewPaymentContextCommand{
			TenantID:           tenantID,
			RegistrationID:     value.ID,
			SeriesID:           seriesID,
			InstanceID:         instanceID,
			SessionID:          sessionID,
			PrincipalID:        value.PrincipalID,
			IdempotencyKey:     key,
			MerchantOrderNo:    key,
			PaymentAppID:       "wx-app",
			PaymentMerchantID:  "wx-merchant",
			OriginalPriceCents: 10_000,
			DiscountCents:      1_000,
			Now:                now.Add(-time.Minute),
		})
		if err != nil {
			t.Fatalf("NewPaymentContext() error = %v", err)
		}
		return created
	}
	paidPayment := newPayment(paidRegistration, "payment:cancel:paid")
	paidPayment.Order, changed, err = payment.ConfirmOrderPayment(
		paidPayment.Order,
		payment.PaymentConfirmation{
			ActualPaidCents:     9_000,
			WeChatTransactionID: "wx-cancel-paid",
			PaidAt:              now.Add(-30 * time.Second),
		},
	)
	if err != nil || !changed {
		t.Fatalf("ConfirmOrderPayment() changed=%t error=%v", changed, err)
	}
	paidPayment.Hold, changed, err = payment.ConvertCapacityHold(
		paidPayment.Hold,
		now.Add(-30*time.Second),
	)
	if err != nil || !changed {
		t.Fatalf("ConvertCapacityHold() changed=%t error=%v", changed, err)
	}
	pendingPayment := newPayment(pendingRegistration, "payment:cancel:pending")

	capacity := 10
	session := activity.Session{
		ID:                         sessionID,
		TenantID:                   tenantID,
		InstanceID:                 instanceID,
		Title:                      "Cancellation fixture",
		Status:                     activity.SessionStatusPublished,
		Capacity:                   &capacity,
		ConfirmedRegistrationCount: 2,
		ActiveHoldCount:            1,
		Version:                    9,
		CreatedAt:                  now.Add(-time.Hour),
		UpdatedAt:                  now,
	}
	fixture := sessionCancellationFixture{
		command: SessionCancellationCommand{
			TenantID:               tenantID,
			SessionID:              sessionID,
			ActorID:                uuid.New(),
			ExpectedSessionVersion: session.Version,
			IdempotencyKey:         "session-cancel:fixture",
			Reason:                 "Unsafe weather",
			NotificationStrategy:   activity.CancellationNotificationManualRequired,
		},
		target: sessionCancellationTarget{
			TenantID:   tenantID,
			SeriesID:   seriesID,
			InstanceID: instanceID,
			SessionID:  sessionID,
		},
		session:        session,
		seriesStatus:   activity.SeriesStatusActive,
		instanceStatus: activity.InstanceStatusPublished,
		registrations: []registration.Registration{
			freeRegistration,
			paidRegistration,
			pendingRegistration,
		},
		freeRegistration:    freeRegistration,
		paidRegistration:    paidRegistration,
		pendingRegistration: pendingRegistration,
		paidOrder:           paidPayment.Order,
		orders: map[uuid.UUID]payment.Order{
			paidRegistration.ID:    paidPayment.Order,
			pendingRegistration.ID: pendingPayment.Order,
		},
		holds: map[uuid.UUID]payment.CapacityHold{
			paidPayment.Order.ID:    paidPayment.Hold,
			pendingPayment.Order.ID: pendingPayment.Hold,
		},
		refunds:     make(map[uuid.UUID]refund.Case),
		coupons:     make(map[uuid.UUID]couponpostgres.Ledger),
		receiptErr:  errSessionCancellationReceiptNotFound,
		processedAt: now.Add(time.Minute),
	}
	refreshSessionCancellationFixturePreview(t, &fixture)
	return fixture
}

func addZeroSettledSessionRegistration(
	t *testing.T,
	fixture *sessionCancellationFixture,
) {
	t.Helper()
	createdAt := fixture.session.UpdatedAt.Add(-time.Minute)
	current, err := registration.NewRegistration(registration.NewRegistrationCommand{
		TenantID:        fixture.target.TenantID,
		SeriesID:        fixture.target.SeriesID,
		InstanceID:      fixture.target.InstanceID,
		SessionID:       fixture.target.SessionID,
		PrincipalID:     uuid.New(),
		IdempotencyKey:  "registration:cancel:zero",
		RequiresPayment: true,
		Now:             createdAt,
	})
	if err != nil {
		t.Fatalf("NewRegistration(zero) error = %v", err)
	}
	paymentContext, err := payment.NewPaymentContext(
		payment.NewPaymentContextCommand{
			TenantID:           current.TenantID,
			RegistrationID:     current.ID,
			SeriesID:           current.SeriesID,
			InstanceID:         current.InstanceID,
			SessionID:          current.SessionID,
			PrincipalID:        current.PrincipalID,
			IdempotencyKey:     "payment:cancel:zero",
			MerchantOrderNo:    "payment:cancel:zero",
			PaymentAppID:       "wx-app",
			PaymentMerchantID:  "wx-merchant",
			OriginalPriceCents: 1_000,
			DiscountCents:      1_000,
			Now:                createdAt,
		},
	)
	if err != nil {
		t.Fatalf("NewPaymentContext(zero) error = %v", err)
	}
	activityType := activity.ActivityTypeAIRoundtable
	grant, err := coupon.NewManualReplenishment(
		coupon.ManualReplenishmentCommand{
			TenantID:    current.TenantID,
			PrincipalID: current.PrincipalID,
			ActorID:     uuid.New(),
			BusinessKey: "session-cancel-zero",
			Reason:      "test Session cancellation",
			Context:     "zero settlement",
			Policy: coupon.GrantPolicy{
				Configured:        true,
				PolicyVersion:     "grant-v1",
				FaceValueCents:    1_000,
				Validity:          24 * time.Hour,
				ScopeType:         coupon.ScopeTypeActivityType,
				ScopeActivityType: &activityType,
			},
			GrantedAt:  createdAt.Add(-time.Minute),
			RecordedAt: createdAt.Add(-time.Minute),
		},
	)
	if err != nil {
		t.Fatalf("NewManualReplenishment(zero) error = %v", err)
	}
	instrument := grant.Coupons[0]
	history := []coupon.Entry{grant.Entries[0]}
	held, err := coupon.Hold(coupon.HoldCommand{
		Instrument:         instrument,
		History:            history,
		OrderID:            paymentContext.Order.ID,
		RegistrationID:     current.ID,
		SeriesID:           current.SeriesID,
		ActivityType:       activityType,
		OriginalPriceCents: paymentContext.Order.OriginalPriceCents,
		HoldExpiresAt:      paymentContext.Hold.ExpiresAt,
		At:                 createdAt,
		RecordedAt:         createdAt,
	})
	if err != nil {
		t.Fatalf("Hold(Coupon zero) error = %v", err)
	}
	history = append(history, held)
	settledAt := fixture.session.UpdatedAt.Add(-30 * time.Second)
	paymentContext.Order, _, err = payment.SettleOrderZero(
		paymentContext.Order,
		settledAt,
	)
	if err != nil {
		t.Fatalf("SettleOrderZero() error = %v", err)
	}
	current, _, err = registration.ConfirmRegistration(current, settledAt)
	if err != nil {
		t.Fatalf("ConfirmRegistration(zero) error = %v", err)
	}
	paymentContext.Hold, _, err = payment.ConvertCapacityHold(
		paymentContext.Hold,
		settledAt,
	)
	if err != nil {
		t.Fatalf("ConvertCapacityHold(zero) error = %v", err)
	}
	redeemed, err := coupon.Redeem(coupon.RedeemCommand{
		Instrument: instrument,
		History:    history,
		OrderID:    paymentContext.Order.ID,
		At:         settledAt,
		RecordedAt: settledAt,
	})
	if err != nil {
		t.Fatalf("Redeem(zero) error = %v", err)
	}
	fixture.registrations = append(fixture.registrations, current)
	fixture.orders[current.ID] = paymentContext.Order
	fixture.holds[paymentContext.Order.ID] = paymentContext.Hold
	fixture.coupons[paymentContext.Order.ID] = couponpostgres.Ledger{
		Instrument: instrument,
		Entries:    append(history, redeemed),
	}
	fixture.session.ConfirmedRegistrationCount++
	refreshSessionCancellationFixturePreview(t, fixture)
}

func refreshSessionCancellationFixturePreview(
	t *testing.T,
	fixture *sessionCancellationFixture,
) {
	t.Helper()
	items := make([]sessionCancellationPlanItem, 0, len(fixture.registrations))
	for _, current := range fixture.registrations {
		item := sessionCancellationPlanItem{registration: current}
		if order, ok := fixture.orders[current.ID]; ok {
			item.order = &order
			hold := fixture.holds[order.ID]
			item.hold = &hold
			if existing, ok := fixture.refunds[order.ID]; ok {
				item.refund = &existing
			}
			if ledger, ok := fixture.coupons[order.ID]; ok {
				item.coupon = &ledger
			}
		}
		items = append(items, item)
	}
	preview, err := activity.NewSessionCancellationPreview(
		activity.SessionCancellationPreviewCommand{
			RequestedBy:    fixture.command.ActorID,
			IdempotencyKey: "session-cancel-preview:fixture",
			At:             fixture.processedAt.Add(-time.Minute),
		},
		sessionCancellationSnapshot(
			fixture.session,
			fixture.target.SeriesID,
			items,
			sessionCancellationCause,
		),
	)
	if err != nil {
		t.Fatalf("NewSessionCancellationPreview() error = %v", err)
	}
	fixture.preview = preview
	fixture.command.PreviewID = preview.ID
	fixture.command.ExpectedSessionVersion = preview.ExpectedSessionVersion
}

func newSessionCancellerHarness(
	fixture sessionCancellationFixture,
) (*SessionCanceller, *fakeSessionCancellationTransaction, *fakeSessionCancellationStarter) {
	tx := &fakeSessionCancellationTransaction{
		seriesStatus:   fixture.seriesStatus,
		instanceStatus: fixture.instanceStatus,
		session:        fixture.session,
		receipt:        fixture.receipt,
		receiptErr:     fixture.receiptErr,
		registrations:  append([]registration.Registration(nil), fixture.registrations...),
		orders:         cloneOrderMap(fixture.orders),
		holds:          cloneHoldMap(fixture.holds),
		refunds:        cloneRefundMap(fixture.refunds),
		coupons:        cloneCouponLedgerMap(fixture.coupons),
		preview:        fixture.preview,
	}
	starter := &fakeSessionCancellationStarter{tx: tx}
	return &SessionCanceller{
		resolver: fakeSessionCancellationResolver{
			target: fixture.target,
			err:    fixture.resolveErr,
		},
		transactions: starter,
		now:          func() time.Time { return fixture.processedAt },
	}, tx, starter
}

type fakeSessionCancellationResolver struct {
	target sessionCancellationTarget
	err    error
}

type fakeActivityCouponPolicy struct {
	decision coupon.RefundPolicyDecision
	input    coupon.RefundPolicyInput
	err      error
	calls    int
}

func (policy *fakeActivityCouponPolicy) EvaluateCouponRefund(
	_ context.Context,
	input coupon.RefundPolicyInput,
) (coupon.RefundPolicyDecision, error) {
	policy.calls++
	policy.input = input
	return policy.decision, policy.err
}

func (resolver fakeSessionCancellationResolver) resolveSessionCancellationTarget(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (sessionCancellationTarget, error) {
	return resolver.target, resolver.err
}

type fakeSessionCancellationStarter struct {
	tx        sessionCancellationTransaction
	isolation sql.IsolationLevel
	started   bool
}

func (starter *fakeSessionCancellationStarter) beginSessionCancellationTx(
	_ context.Context,
	options *sql.TxOptions,
) (sessionCancellationTransaction, error) {
	starter.started = true
	starter.isolation = options.Isolation
	return starter.tx, nil
}

type fakeSessionCancellationTransaction struct {
	seriesStatus            activity.SeriesStatus
	instanceStatus          activity.InstanceStatus
	session                 activity.Session
	receipt                 activity.SessionCancellationReceipt
	receiptErr              error
	registrations           []registration.Registration
	orders                  map[uuid.UUID]payment.Order
	holds                   map[uuid.UUID]payment.CapacityHold
	refunds                 map[uuid.UUID]refund.Case
	coupons                 map[uuid.UUID]couponpostgres.Ledger
	preview                 activity.SessionCancellationPreview
	lockOrder               []string
	listRegistrationsCalled bool
	registrationUpdates     []registration.Registration
	orderUpdates            []payment.Order
	holdUpdates             []payment.CapacityHold
	couponReleases          []uuid.UUID
	couponAppends           []coupon.Entry
	refundCreates           []refund.Case
	sessionUpdated          activity.Session
	receiptCreated          activity.SessionCancellationReceipt
	previewCreated          activity.SessionCancellationPreview
	previewConsumed         activity.SessionCancellationPreview
	committed               bool
	rolledBack              bool
}

func (tx *fakeSessionCancellationTransaction) lockSeries(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (activity.SeriesStatus, error) {
	tx.lockOrder = append(tx.lockOrder, "series")
	return tx.seriesStatus, nil
}

func (tx *fakeSessionCancellationTransaction) lockInstance(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (activity.InstanceStatus, error) {
	tx.lockOrder = append(tx.lockOrder, "instance")
	return tx.instanceStatus, nil
}

func (tx *fakeSessionCancellationTransaction) lockSession(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (activity.Session, error) {
	tx.lockOrder = append(tx.lockOrder, "session")
	return tx.session, nil
}

func (tx *fakeSessionCancellationTransaction) getReceiptBySession(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (activity.SessionCancellationReceipt, error) {
	return tx.receipt, tx.receiptErr
}

func (tx *fakeSessionCancellationTransaction) getPreviewByKey(
	_ context.Context,
	_ uuid.UUID,
	idempotencyKey string,
) (activity.SessionCancellationPreview, error) {
	if tx.preview.ID == uuid.Nil || tx.preview.IdempotencyKey != idempotencyKey {
		return activity.SessionCancellationPreview{},
			errSessionCancellationPreviewNotFound
	}
	return tx.preview, nil
}

func (tx *fakeSessionCancellationTransaction) lockPreview(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (activity.SessionCancellationPreview, error) {
	if tx.preview.ID == uuid.Nil {
		return activity.SessionCancellationPreview{},
			errSessionCancellationPreviewNotFound
	}
	tx.lockOrder = append(tx.lockOrder, "preview")
	return tx.preview, nil
}

func (tx *fakeSessionCancellationTransaction) listOpenRegistrations(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) ([]registration.Registration, error) {
	tx.listRegistrationsCalled = true
	tx.lockOrder = append(tx.lockOrder, "registrations")
	return append([]registration.Registration(nil), tx.registrations...), nil
}

func (tx *fakeSessionCancellationTransaction) lockOrderByRegistration(
	_ context.Context,
	_ uuid.UUID,
	registrationID uuid.UUID,
) (payment.Order, bool, error) {
	tx.lockOrder = append(tx.lockOrder, "order")
	value, ok := tx.orders[registrationID]
	return value, ok, nil
}

func (tx *fakeSessionCancellationTransaction) lockHoldByOrder(
	_ context.Context,
	_ uuid.UUID,
	orderID uuid.UUID,
) (payment.CapacityHold, error) {
	tx.lockOrder = append(tx.lockOrder, "hold")
	value, ok := tx.holds[orderID]
	if !ok {
		return payment.CapacityHold{}, ErrSessionCancellationTransaction
	}
	return value, nil
}

func (tx *fakeSessionCancellationTransaction) lockRefundByOrder(
	_ context.Context,
	_ uuid.UUID,
	orderID uuid.UUID,
) (refund.Case, error) {
	tx.lockOrder = append(tx.lockOrder, "refund")
	value, ok := tx.refunds[orderID]
	if !ok {
		return refund.Case{}, errSessionCancellationRefundNotFound
	}
	return value, nil
}

func (tx *fakeSessionCancellationTransaction) lockCouponByOrder(
	_ context.Context,
	_ uuid.UUID,
	orderID uuid.UUID,
) (couponpostgres.Ledger, error) {
	tx.lockOrder = append(tx.lockOrder, "coupon")
	value, ok := tx.coupons[orderID]
	if !ok {
		return couponpostgres.Ledger{}, ErrSessionCancellationTransaction
	}
	return value, nil
}

func (tx *fakeSessionCancellationTransaction) updateRegistration(
	_ context.Context,
	value registration.Registration,
	_ int64,
) (registration.Registration, error) {
	tx.registrationUpdates = append(tx.registrationUpdates, value)
	return value, nil
}

func (tx *fakeSessionCancellationTransaction) updateOrder(
	_ context.Context,
	value payment.Order,
	_ int64,
) (payment.Order, error) {
	tx.orderUpdates = append(tx.orderUpdates, value)
	return value, nil
}

func (tx *fakeSessionCancellationTransaction) updateHold(
	_ context.Context,
	value payment.CapacityHold,
	_ int64,
) (payment.CapacityHold, error) {
	tx.holdUpdates = append(tx.holdUpdates, value)
	return value, nil
}

func (tx *fakeSessionCancellationTransaction) releaseCouponForOrder(
	_ context.Context,
	_ uuid.UUID,
	orderID uuid.UUID,
	_ string,
	_ time.Time,
) error {
	tx.couponReleases = append(tx.couponReleases, orderID)
	return nil
}

func (tx *fakeSessionCancellationTransaction) appendCouponEntry(
	_ context.Context,
	value coupon.Entry,
) (coupon.Entry, error) {
	tx.couponAppends = append(tx.couponAppends, value)
	return value, nil
}

func (tx *fakeSessionCancellationTransaction) createRefund(
	_ context.Context,
	value refund.Case,
) (refund.Case, error) {
	tx.refundCreates = append(tx.refundCreates, value)
	tx.refunds[value.OrderID] = value
	return value, nil
}

func (tx *fakeSessionCancellationTransaction) updateSession(
	_ context.Context,
	value activity.Session,
	_ int64,
) (activity.Session, error) {
	tx.sessionUpdated = value
	return value, nil
}

func (tx *fakeSessionCancellationTransaction) createReceipt(
	_ context.Context,
	value activity.SessionCancellationReceipt,
) (activity.SessionCancellationReceipt, error) {
	tx.receiptCreated = value
	return value, nil
}

func (tx *fakeSessionCancellationTransaction) createPreview(
	_ context.Context,
	value activity.SessionCancellationPreview,
) (activity.SessionCancellationPreview, error) {
	tx.preview = value
	tx.previewCreated = value
	return value, nil
}

func (tx *fakeSessionCancellationTransaction) consumePreview(
	_ context.Context,
	_ uuid.UUID,
	_ uuid.UUID,
	consumedAt time.Time,
) (activity.SessionCancellationPreview, error) {
	if tx.preview.ID == uuid.Nil || tx.preview.ConsumedAt != nil ||
		consumedAt.Before(tx.preview.CreatedAt) ||
		consumedAt.After(tx.preview.ExpiresAt) {
		return activity.SessionCancellationPreview{},
			ErrSessionCancellationPreviewConflict
	}
	tx.preview.ConsumedAt = &consumedAt
	tx.previewConsumed = tx.preview
	return tx.preview, nil
}

func (tx *fakeSessionCancellationTransaction) Commit() error {
	tx.committed = true
	return nil
}

func (tx *fakeSessionCancellationTransaction) Rollback() error {
	tx.rolledBack = true
	return nil
}

func cloneOrderMap(source map[uuid.UUID]payment.Order) map[uuid.UUID]payment.Order {
	cloned := make(map[uuid.UUID]payment.Order, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneHoldMap(
	source map[uuid.UUID]payment.CapacityHold,
) map[uuid.UUID]payment.CapacityHold {
	cloned := make(map[uuid.UUID]payment.CapacityHold, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneRefundMap(source map[uuid.UUID]refund.Case) map[uuid.UUID]refund.Case {
	cloned := make(map[uuid.UUID]refund.Case, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneCouponLedgerMap(
	source map[uuid.UUID]couponpostgres.Ledger,
) map[uuid.UUID]couponpostgres.Ledger {
	cloned := make(map[uuid.UUID]couponpostgres.Ledger, len(source))
	for key, value := range source {
		value.Entries = append([]coupon.Entry(nil), value.Entries...)
		cloned[key] = value
	}
	return cloned
}

func sameStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
