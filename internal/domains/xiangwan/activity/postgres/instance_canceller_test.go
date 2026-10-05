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
)

func TestInstanceCancellationPreviewAndCommitConvergeWholeInstance(t *testing.T) {
	t.Parallel()

	fixture := newInstanceCancellationFixture(t)
	previewer, previewTx, previewStarter := newInstancePreviewerHarness(fixture)
	preview, err := previewer.Preview(context.Background(), fixture.previewCommand)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if previewStarter.isolation != sql.LevelSerializable ||
		!previewTx.committed ||
		preview.SessionCount != 3 ||
		preview.TargetSessionCount != 2 ||
		preview.AlreadyCancelledSessionCount != 1 ||
		preview.CancelledRegistrationCount != 3 ||
		preview.ConfirmedRegistrationCount != 2 ||
		preview.ActiveHoldCount != 1 ||
		preview.FreeRegistrationCount != 1 ||
		preview.PaidRefundRegistrationCount != 1 ||
		preview.PendingOrderCount != 1 ||
		preview.RefundCaseCount != 1 ||
		preview.RequestedRefundCents != 9_000 ||
		preview.CancellationReason != fixture.previewCommand.Reason ||
		len(preview.SessionImpacts) != 3 {
		t.Fatalf("Instance preview=%+v tx=%+v", preview, previewTx)
	}
	if len(previewTx.registrationUpdates) != 0 ||
		len(previewTx.updatedSessions) != 0 ||
		previewTx.updatedInstance.ID != uuid.Nil {
		t.Fatalf("preview mutated facts: %+v", previewTx)
	}

	fixture.preview = preview
	fixture.command.PreviewID = preview.ID
	fixture.command.ExpectedInstanceVersion = preview.ExpectedInstanceVersion
	fixture.command.NotificationStrategy = preview.NotificationStrategy
	canceller, tx, starter := newInstanceCancellerHarness(fixture)
	result, err := canceller.Cancel(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if starter.isolation != sql.LevelSerializable ||
		!tx.committed ||
		tx.rolledBack ||
		result.Instance.Status != activity.InstanceStatusCancelled ||
		result.Instance.Version != fixture.instance.Version+1 {
		t.Fatalf("Instance cancellation result=%+v tx=%+v", result, tx)
	}
	if result.Receipt.SessionCount != 3 ||
		result.Receipt.NewlyCancelledSessionCount != 2 ||
		result.Receipt.AlreadyCancelledSessionCount != 1 ||
		result.Receipt.CancelledRegistrationCount != 3 ||
		result.Receipt.ReleasedConfirmedCount != 2 ||
		result.Receipt.ReleasedHoldCount != 1 ||
		result.Receipt.ClosedPendingOrderCount != 1 ||
		result.Receipt.RefundCaseCount != 1 ||
		result.Receipt.RequestedRefundCents != 9_000 ||
		len(result.SessionReceipts) != 2 ||
		len(tx.updatedSessions) != 2 ||
		len(tx.childPreviewsCreated) != 2 ||
		len(tx.childReceiptsCreated) != 2 {
		t.Fatalf("Instance cancellation receipts=%+v tx=%+v", result, tx)
	}
	for _, updated := range tx.registrationUpdates {
		if updated.ParticipationStatus != registration.ParticipationStatusCancelled ||
			updated.CancellationReason == nil ||
			*updated.CancellationReason != "instance_cancelled" {
			t.Fatalf("cancelled Registration = %+v", updated)
		}
	}
	if len(tx.refundCreates) != 1 ||
		tx.refundCreates[0].ReasonCode != refund.ReasonInstanceCancelled {
		t.Fatalf("Instance Refund cases = %+v", tx.refundCreates)
	}
	if tx.instancePreviewConsumed.ConsumedAt == nil ||
		tx.instanceReceiptCreated.ID != result.Receipt.ID {
		t.Fatalf("Instance preview/receipt tx = %+v", tx)
	}
}

func TestInstanceCancellerRechecksAuthorizationInsideTransaction(t *testing.T) {
	t.Parallel()

	fixture := newInstanceCancellationFixture(t)
	fixture.command.PreviewID = uuid.New()
	fixture.command.ExpectedInstanceVersion = fixture.instance.Version
	fixture.command.NotificationStrategy = activity.CancellationNotificationManualRequired
	canceller, tx, starter := newInstanceCancellerHarness(fixture)
	tx.authorizationErr = errors.New("administrator grant revoked")
	_, err := canceller.Cancel(context.Background(), fixture.command)
	if !errors.Is(err, tx.authorizationErr) || !starter.started ||
		tx.committed || !tx.rolledBack || len(tx.lockOrder) != 1 ||
		tx.lockOrder[0] != "authorization" {
		t.Fatalf("authorization recheck err=%v tx=%+v", err, tx)
	}
}

func TestInstanceCancellationPreviewRechecksAuthorizationInsideTransaction(t *testing.T) {
	t.Parallel()

	fixture := newInstanceCancellationFixture(t)
	previewer, tx, starter := newInstancePreviewerHarness(fixture)
	tx.authorizationErr = errors.New("administrator grant revoked")
	_, err := previewer.Preview(context.Background(), fixture.previewCommand)
	if !errors.Is(err, tx.authorizationErr) || !starter.started ||
		tx.committed || !tx.rolledBack || len(tx.lockOrder) != 1 ||
		tx.lockOrder[0] != "authorization" {
		t.Fatalf("preview authorization recheck err=%v tx=%+v", err, tx)
	}
}

func TestInstanceCancellerPropagatesZeroSettlementCouponPolicy(t *testing.T) {
	t.Parallel()

	fixture := newInstanceCancellationFixture(t)
	addZeroSettledInstanceRegistration(t, &fixture)
	previewer, _, _ := newInstancePreviewerHarness(fixture)
	preview, err := previewer.Preview(context.Background(), fixture.previewCommand)
	if err != nil {
		t.Fatalf("Preview(zero settlement) error = %v", err)
	}
	if preview.CouponAdjustmentCount != 1 ||
		preview.ConfirmedRegistrationCount != 3 ||
		preview.CancelledRegistrationCount != 4 {
		t.Fatalf("Preview(zero settlement) = %+v", preview)
	}
	fixture.preview = preview
	fixture.command.PreviewID = preview.ID
	fixture.command.ExpectedInstanceVersion = preview.ExpectedInstanceVersion
	fixture.command.NotificationStrategy = preview.NotificationStrategy
	canceller, tx, _ := newInstanceCancellerHarness(fixture)
	policy := &fakeActivityCouponPolicy{
		decision: coupon.RefundPolicyDecision{
			Configured:    true,
			PolicyVersion: "coupon-instance-cancel-v1",
			Disposition:   coupon.RefundDispositionForfeit,
		},
	}
	canceller.couponPolicy = policy

	result, err := canceller.Cancel(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf("Cancel(zero settlement) error = %v", err)
	}
	if result.Receipt.CouponAdjustmentCount != 1 ||
		len(tx.couponAppends) != 1 ||
		tx.couponAppends[0].EntryType != coupon.EntryTypeForfeited ||
		policy.calls != 1 || !tx.committed || tx.rolledBack {
		t.Fatalf(
			"Cancel(zero settlement)=%+v policy=%+v tx=%+v",
			result,
			policy,
			tx,
		)
	}
}

func TestInstanceCancellationPreviewExactReplayAndConflict(t *testing.T) {
	t.Parallel()

	fixture := newInstanceCancellationFixture(t)
	preview, err := activity.NewInstanceCancellationPreview(
		activity.InstanceCancellationPreviewCommand{
			RequestedBy:    fixture.previewCommand.RequestedBy,
			IdempotencyKey: fixture.previewCommand.IdempotencyKey,
			Reason:         fixture.previewCommand.Reason,
			At:             fixture.processedAt.Add(-time.Minute),
		},
		fixture.snapshot(t),
	)
	if err != nil {
		t.Fatalf("NewInstanceCancellationPreview() error = %v", err)
	}
	fixture.preview = preview
	previewer, tx, _ := newInstancePreviewerHarness(fixture)
	replayed, err := previewer.Preview(context.Background(), fixture.previewCommand)
	if err != nil || replayed.ID != preview.ID || !tx.committed {
		t.Fatalf("Preview(replay)=%+v tx=%+v err=%v", replayed, tx, err)
	}

	conflicting := fixture
	conflicting.previewCommand.RequestedBy = uuid.New()
	previewer, tx, _ = newInstancePreviewerHarness(conflicting)
	_, err = previewer.Preview(context.Background(), conflicting.previewCommand)
	if !errors.Is(err, ErrInstanceCancellationPreviewConflict) ||
		tx.committed ||
		!tx.rolledBack {
		t.Fatalf("Preview(conflict) tx=%+v err=%v", tx, err)
	}
}

func TestInstanceCancellerRejectsStalePreviewWithoutPartialWrites(t *testing.T) {
	t.Parallel()

	fixture := newInstanceCancellationFixture(t)
	preview, err := activity.NewInstanceCancellationPreview(
		activity.InstanceCancellationPreviewCommand{
			RequestedBy:    fixture.command.ActorID,
			IdempotencyKey: fixture.previewCommand.IdempotencyKey,
			Reason:         fixture.command.Reason,
			At:             fixture.processedAt.Add(-time.Minute),
		},
		fixture.snapshot(t),
	)
	if err != nil {
		t.Fatalf("NewInstanceCancellationPreview() error = %v", err)
	}
	fixture.preview = preview
	fixture.command.PreviewID = preview.ID
	fixture.command.ExpectedInstanceVersion = preview.ExpectedInstanceVersion
	fixture.command.NotificationStrategy = preview.NotificationStrategy
	order := fixture.orders[fixture.base.pendingRegistration.ID]
	order.PaymentStatus = payment.OrderStatusUnknown
	order.Version++
	fixture.orders[fixture.base.pendingRegistration.ID] = order
	canceller, tx, _ := newInstanceCancellerHarness(fixture)

	_, err = canceller.Cancel(context.Background(), fixture.command)
	if !errors.Is(err, ErrInstanceCancellationPreviewConflict) {
		t.Fatalf("Cancel(stale) error = %v", err)
	}
	if tx.committed ||
		!tx.rolledBack ||
		len(tx.registrationUpdates) != 0 ||
		len(tx.updatedSessions) != 0 ||
		tx.updatedInstance.ID != uuid.Nil {
		t.Fatalf("stale cancellation transaction = %+v", tx)
	}
}

func TestInstanceCancellerExactReplayReturnsAggregateReceipt(t *testing.T) {
	t.Parallel()

	fixture := newInstanceCancellationFixture(t)
	preview, err := activity.NewInstanceCancellationPreview(
		activity.InstanceCancellationPreviewCommand{
			RequestedBy:    fixture.command.ActorID,
			IdempotencyKey: fixture.previewCommand.IdempotencyKey,
			Reason:         fixture.command.Reason,
			At:             fixture.processedAt.Add(-time.Minute),
		},
		fixture.snapshot(t),
	)
	if err != nil {
		t.Fatalf("NewInstanceCancellationPreview() error = %v", err)
	}
	fixture.preview = preview
	fixture.command.PreviewID = preview.ID
	fixture.command.ExpectedInstanceVersion = preview.ExpectedInstanceVersion
	fixture.command.NotificationStrategy = preview.NotificationStrategy
	fixture.instance.Status = activity.InstanceStatusCancelled
	fixture.instance.Version++
	fixture.instance.UpdatedAt = fixture.processedAt
	fixture.receipt = activity.InstanceCancellationReceipt{
		ID:                       uuid.New(),
		TenantID:                 fixture.command.TenantID,
		SeriesID:                 fixture.target.SeriesID,
		InstanceID:               fixture.command.InstanceID,
		PreviewID:                fixture.command.PreviewID,
		IdempotencyKey:           fixture.command.IdempotencyKey,
		CancelledBy:              fixture.command.ActorID,
		CancellationReason:       fixture.command.Reason,
		NotificationStrategy:     fixture.command.NotificationStrategy,
		ResultingInstanceVersion: fixture.instance.Version,
	}
	fixture.receiptErr = nil
	canceller, tx, _ := newInstanceCancellerHarness(fixture)

	result, err := canceller.Cancel(context.Background(), fixture.command)
	if err != nil ||
		result.Receipt.ID != fixture.receipt.ID ||
		!tx.committed ||
		tx.listSessionsCalled {
		t.Fatalf("Cancel(replay)=%+v tx=%+v err=%v", result, tx, err)
	}
}

type instanceCancellationFixture struct {
	base           sessionCancellationFixture
	target         instanceCancellationTarget
	instance       activity.Instance
	sessions       []activity.Session
	registrations  map[uuid.UUID][]registration.Registration
	orders         map[uuid.UUID]payment.Order
	holds          map[uuid.UUID]payment.CapacityHold
	refunds        map[uuid.UUID]refund.Case
	coupons        map[uuid.UUID]couponpostgres.Ledger
	previewCommand PreviewInstanceCancellationCommand
	command        InstanceCancellationCommand
	preview        activity.InstanceCancellationPreview
	receipt        activity.InstanceCancellationReceipt
	receiptErr     error
	processedAt    time.Time
}

func newInstanceCancellationFixture(t *testing.T) instanceCancellationFixture {
	t.Helper()
	base := newSessionCancellationFixture(t)
	empty := base.session
	empty.ID = uuid.New()
	empty.Title = "Empty sibling"
	empty.ConfirmedRegistrationCount = 0
	empty.ActiveHoldCount = 0
	empty.Version = 4
	cancelled := base.session
	cancelled.ID = uuid.New()
	cancelled.Title = "Already cancelled sibling"
	cancelled.Status = activity.SessionStatusCancelled
	cancelled.ConfirmedRegistrationCount = 0
	cancelled.ActiveHoldCount = 0
	cancelled.Version = 3
	instance := activity.Instance{
		ID:        base.target.InstanceID,
		TenantID:  base.target.TenantID,
		SeriesID:  base.target.SeriesID,
		Title:     "September gathering",
		Status:    activity.InstanceStatusPublished,
		Version:   8,
		CreatedAt: base.session.CreatedAt,
		UpdatedAt: base.session.UpdatedAt,
	}
	actorID := uuid.New()
	return instanceCancellationFixture{
		base: base,
		target: instanceCancellationTarget{
			TenantID:   instance.TenantID,
			SeriesID:   instance.SeriesID,
			InstanceID: instance.ID,
		},
		instance: instance,
		sessions: []activity.Session{base.session, empty, cancelled},
		registrations: map[uuid.UUID][]registration.Registration{
			base.session.ID: append(
				[]registration.Registration(nil),
				base.registrations...,
			),
		},
		orders:  cloneOrderMap(base.orders),
		holds:   cloneHoldMap(base.holds),
		refunds: cloneRefundMap(base.refunds),
		coupons: cloneCouponLedgerMap(base.coupons),
		previewCommand: PreviewInstanceCancellationCommand{
			TenantID:       instance.TenantID,
			InstanceID:     instance.ID,
			RequestedBy:    actorID,
			IdempotencyKey: "instance-cancel-preview:fixture",
			Reason:         "Venue unavailable",
		},
		command: InstanceCancellationCommand{
			TenantID:       instance.TenantID,
			InstanceID:     instance.ID,
			ActorID:        actorID,
			IdempotencyKey: "instance-cancel:fixture",
			Reason:         "Venue unavailable",
		},
		receiptErr:  errInstanceCancellationReceiptNotFound,
		processedAt: base.processedAt,
	}
}

func addZeroSettledInstanceRegistration(
	t *testing.T,
	fixture *instanceCancellationFixture,
) {
	t.Helper()
	base := fixture.base
	addZeroSettledSessionRegistration(t, &base)
	fixture.base = base
	fixture.sessions[0] = base.session
	fixture.registrations[base.session.ID] = append(
		[]registration.Registration(nil),
		base.registrations...,
	)
	fixture.orders = cloneOrderMap(base.orders)
	fixture.holds = cloneHoldMap(base.holds)
	fixture.refunds = cloneRefundMap(base.refunds)
	fixture.coupons = cloneCouponLedgerMap(base.coupons)
}

func (fixture instanceCancellationFixture) snapshot(
	t *testing.T,
) activity.InstanceCancellationSnapshot {
	t.Helper()
	tx := newFakeInstanceCancellationTransaction(fixture)
	plan, err := buildInstanceCancellationPlan(
		context.Background(),
		tx,
		fixture.instance,
		fixture.sessions,
	)
	if err != nil {
		t.Fatalf("buildInstanceCancellationPlan() error = %v", err)
	}
	return plan.snapshot
}

func newInstancePreviewerHarness(
	fixture instanceCancellationFixture,
) (
	*InstanceCancellationPreviewer,
	*fakeInstanceCancellationTransaction,
	*fakeInstanceCancellationStarter,
) {
	tx := newFakeInstanceCancellationTransaction(fixture)
	starter := &fakeInstanceCancellationStarter{tx: tx}
	return &InstanceCancellationPreviewer{
		resolver:     fakeInstanceCancellationResolver{target: fixture.target},
		transactions: starter,
		now:          func() time.Time { return fixture.processedAt },
	}, tx, starter
}

func newInstanceCancellerHarness(
	fixture instanceCancellationFixture,
) (
	*InstanceCanceller,
	*fakeInstanceCancellationTransaction,
	*fakeInstanceCancellationStarter,
) {
	tx := newFakeInstanceCancellationTransaction(fixture)
	starter := &fakeInstanceCancellationStarter{tx: tx}
	return &InstanceCanceller{
		resolver:     fakeInstanceCancellationResolver{target: fixture.target},
		transactions: starter,
		now:          func() time.Time { return fixture.processedAt },
	}, tx, starter
}

type fakeInstanceCancellationResolver struct {
	target instanceCancellationTarget
	err    error
}

func (resolver fakeInstanceCancellationResolver) resolveInstanceCancellationTarget(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (instanceCancellationTarget, error) {
	return resolver.target, resolver.err
}

type fakeInstanceCancellationStarter struct {
	tx        instanceCancellationTransaction
	isolation sql.IsolationLevel
	started   bool
}

func (starter *fakeInstanceCancellationStarter) beginInstanceCancellationTx(
	_ context.Context,
	options *sql.TxOptions,
) (instanceCancellationTransaction, error) {
	starter.started = true
	starter.isolation = options.Isolation
	return starter.tx, nil
}

type fakeInstanceCancellationTransaction struct {
	*fakeSessionCancellationTransaction
	instance                activity.Instance
	sessions                []activity.Session
	registrationsBySession  map[uuid.UUID][]registration.Registration
	instancePreview         activity.InstanceCancellationPreview
	instancePreviewCreated  activity.InstanceCancellationPreview
	instancePreviewConsumed activity.InstanceCancellationPreview
	instanceReceipt         activity.InstanceCancellationReceipt
	instanceReceiptErr      error
	instanceReceiptCreated  activity.InstanceCancellationReceipt
	updatedInstance         activity.Instance
	updatedSessions         []activity.Session
	childPreviewsCreated    []activity.SessionCancellationPreview
	childReceiptsCreated    []activity.SessionCancellationReceipt
	listSessionsCalled      bool
	authorizationErr        error
}

func (tx *fakeInstanceCancellationTransaction) authorizeInstanceCancellation(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) error {
	tx.lockOrder = append(tx.lockOrder, "authorization")
	return tx.authorizationErr
}

func newFakeInstanceCancellationTransaction(
	fixture instanceCancellationFixture,
) *fakeInstanceCancellationTransaction {
	base := &fakeSessionCancellationTransaction{
		seriesStatus: fixture.base.seriesStatus,
		orders:       cloneOrderMap(fixture.orders),
		holds:        cloneHoldMap(fixture.holds),
		refunds:      cloneRefundMap(fixture.refunds),
		coupons:      cloneCouponLedgerMap(fixture.coupons),
		receiptErr:   errSessionCancellationReceiptNotFound,
	}
	registrations := make(map[uuid.UUID][]registration.Registration)
	for sessionID, values := range fixture.registrations {
		registrations[sessionID] = append(
			[]registration.Registration(nil),
			values...,
		)
	}
	return &fakeInstanceCancellationTransaction{
		fakeSessionCancellationTransaction: base,
		instance:                           fixture.instance,
		sessions: append(
			[]activity.Session(nil),
			fixture.sessions...,
		),
		registrationsBySession: registrations,
		instancePreview:        fixture.preview,
		instanceReceipt:        fixture.receipt,
		instanceReceiptErr:     fixture.receiptErr,
	}
}

func (tx *fakeInstanceCancellationTransaction) lockInstanceRecord(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (activity.Instance, error) {
	tx.lockOrder = append(tx.lockOrder, "instance")
	return tx.instance, nil
}

func (tx *fakeInstanceCancellationTransaction) listInstanceSessions(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) ([]activity.Session, error) {
	tx.listSessionsCalled = true
	tx.lockOrder = append(tx.lockOrder, "sessions")
	return append([]activity.Session(nil), tx.sessions...), nil
}

func (tx *fakeInstanceCancellationTransaction) listOpenRegistrations(
	_ context.Context,
	_ uuid.UUID,
	sessionID uuid.UUID,
) ([]registration.Registration, error) {
	tx.listRegistrationsCalled = true
	tx.lockOrder = append(tx.lockOrder, "registrations")
	return append(
		[]registration.Registration(nil),
		tx.registrationsBySession[sessionID]...,
	), nil
}

func (tx *fakeInstanceCancellationTransaction) getInstancePreviewByKey(
	_ context.Context,
	_ uuid.UUID,
	idempotencyKey string,
) (activity.InstanceCancellationPreview, error) {
	if tx.instancePreview.ID == uuid.Nil ||
		tx.instancePreview.IdempotencyKey != idempotencyKey {
		return activity.InstanceCancellationPreview{},
			errInstanceCancellationPreviewNotFound
	}
	return tx.instancePreview, nil
}

func (tx *fakeInstanceCancellationTransaction) lockInstancePreview(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (activity.InstanceCancellationPreview, error) {
	if tx.instancePreview.ID == uuid.Nil {
		return activity.InstanceCancellationPreview{},
			errInstanceCancellationPreviewNotFound
	}
	return tx.instancePreview, nil
}

func (tx *fakeInstanceCancellationTransaction) createInstancePreview(
	_ context.Context,
	value activity.InstanceCancellationPreview,
) (activity.InstanceCancellationPreview, error) {
	tx.instancePreview = value
	tx.instancePreviewCreated = value
	return value, nil
}

func (tx *fakeInstanceCancellationTransaction) consumeInstancePreview(
	_ context.Context,
	_ uuid.UUID,
	_ uuid.UUID,
	consumedAt time.Time,
) (activity.InstanceCancellationPreview, error) {
	if tx.instancePreview.ID == uuid.Nil || tx.instancePreview.ConsumedAt != nil {
		return activity.InstanceCancellationPreview{},
			ErrInstanceCancellationPreviewConflict
	}
	tx.instancePreview.ConsumedAt = &consumedAt
	tx.instancePreviewConsumed = tx.instancePreview
	return tx.instancePreview, nil
}

func (tx *fakeInstanceCancellationTransaction) getInstanceReceipt(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (activity.InstanceCancellationReceipt, error) {
	return tx.instanceReceipt, tx.instanceReceiptErr
}

func (tx *fakeInstanceCancellationTransaction) updateCancelledInstance(
	_ context.Context,
	value activity.Instance,
	_ int64,
) (activity.Instance, error) {
	tx.instance = value
	tx.updatedInstance = value
	return value, nil
}

func (tx *fakeInstanceCancellationTransaction) createInstanceReceipt(
	_ context.Context,
	value activity.InstanceCancellationReceipt,
) (activity.InstanceCancellationReceipt, error) {
	tx.instanceReceipt = value
	tx.instanceReceiptCreated = value
	return value, nil
}

func (tx *fakeInstanceCancellationTransaction) updateSession(
	_ context.Context,
	value activity.Session,
	_ int64,
) (activity.Session, error) {
	tx.updatedSessions = append(tx.updatedSessions, value)
	return value, nil
}

func (tx *fakeInstanceCancellationTransaction) createPreview(
	_ context.Context,
	value activity.SessionCancellationPreview,
) (activity.SessionCancellationPreview, error) {
	tx.childPreviewsCreated = append(tx.childPreviewsCreated, value)
	return value, nil
}

func (tx *fakeInstanceCancellationTransaction) consumePreview(
	_ context.Context,
	_ uuid.UUID,
	previewID uuid.UUID,
	consumedAt time.Time,
) (activity.SessionCancellationPreview, error) {
	for index := range tx.childPreviewsCreated {
		if tx.childPreviewsCreated[index].ID == previewID {
			tx.childPreviewsCreated[index].ConsumedAt = &consumedAt
			return tx.childPreviewsCreated[index], nil
		}
	}
	return activity.SessionCancellationPreview{},
		ErrInstanceCancellationTransaction
}

func (tx *fakeInstanceCancellationTransaction) createReceipt(
	_ context.Context,
	value activity.SessionCancellationReceipt,
) (activity.SessionCancellationReceipt, error) {
	tx.childReceiptsCreated = append(tx.childReceiptsCreated, value)
	return value, nil
}
