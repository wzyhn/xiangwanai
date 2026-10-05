package paymentpostgres

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
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCapacityHoldExpirerClosesPendingContextAndReleasesCapacity(t *testing.T) {
	t.Parallel()

	current, processedAt := dueCapacityHoldFixture(t)
	expirer, tx, capture := newCapacityHoldExpirerHarness(
		t,
		current,
		processedAt,
		capacityHoldExpirerScenario{},
	)

	expired, err := expirer.ExpireDue(context.Background(), 1)
	if err != nil {
		t.Fatalf("ExpireDue() error = %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("ExpireDue() length = %d, want 1", len(expired))
	}
	result := expired[0]
	if result.Order.PaymentStatus != payment.OrderStatusClosedUnpaid ||
		result.Hold.HoldStatus != payment.CapacityHoldStatusExpired ||
		result.Registration.ParticipationStatus != registration.ParticipationStatusCancelled {
		t.Fatalf("ExpireDue() = %+v", result)
	}
	if !tx.committed || tx.rolledBack || capture.isolation != sql.LevelSerializable {
		t.Fatalf("transaction/capture = %+v %+v", tx, capture)
	}
	if !reflect.DeepEqual(capture.lockOrder, []string{
		"series", "instance", "session", "registration", "order", "hold",
	}) {
		t.Fatalf("lock order = %v", capture.lockOrder)
	}
	if capture.orderUpdates != 1 ||
		capture.holdUpdates != 1 ||
		capture.registrationUpdates != 1 ||
		capture.capacityDecrements != 1 {
		t.Fatalf("write counts = %+v", capture)
	}
}

func TestCapacityHoldExpirerReleasesSelectedCouponInSameTransaction(t *testing.T) {
	t.Parallel()

	current, processedAt := dueCapacityHoldFixture(t)
	startCommand := validPaidRegistrationCommand()
	startCommand.TenantID = current.Payment.Order.TenantID
	startCommand.PrincipalID = current.Payment.Order.PrincipalID
	ledger := paidRegistrarCouponLedger(
		t,
		startCommand,
		current.Payment.Order.CreatedAt,
		1_000,
	)
	current.Payment.Order.DiscountCents = ledger.Instrument.FaceValueCents
	current.Payment.Order.PayableCents =
		current.Payment.Order.OriginalPriceCents - ledger.Instrument.FaceValueCents
	held, err := coupon.Hold(coupon.HoldCommand{
		Instrument:         ledger.Instrument,
		History:            ledger.Entries,
		OrderID:            current.Payment.Order.ID,
		RegistrationID:     current.Registration.ID,
		SeriesID:           current.Registration.SeriesID,
		ActivityType:       activity.ActivityTypeAIRoundtable,
		OriginalPriceCents: current.Payment.Order.OriginalPriceCents,
		HoldExpiresAt:      current.Payment.Hold.ExpiresAt,
		At:                 current.Payment.Order.CreatedAt,
		RecordedAt:         current.Payment.Order.CreatedAt,
	})
	if err != nil {
		t.Fatalf("coupon.Hold() error = %v", err)
	}
	ledger.Entries = append(ledger.Entries, held)
	couponRepository := &fakeCouponOrderRepository{ledger: ledger}
	expirer, tx, _ := newCapacityHoldExpirerHarness(
		t,
		current,
		processedAt,
		capacityHoldExpirerScenario{},
	)
	tx.coupons = couponRepository

	expired, err := expirer.ExpireDue(context.Background(), 1)
	if err != nil {
		t.Fatalf("ExpireDue(Coupon) error = %v", err)
	}
	if len(expired) != 1 || expired[0].Coupon == nil ||
		expired[0].Coupon.Entry.EntryType != coupon.EntryTypeReleased ||
		len(couponRepository.appends) != 1 || !tx.committed {
		t.Fatalf("ExpireDue(Coupon) = %+v appends=%+v", expired, couponRepository.appends)
	}
}

func TestCapacityHoldExpirerPreservesUnknownPaymentOutcome(t *testing.T) {
	t.Parallel()

	current, processedAt := dueCapacityHoldFixture(t)
	unknownOrder, _, err := payment.MarkOrderUnknown(
		current.Payment.Order,
		current.Payment.Order.CreatedAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("MarkOrderUnknown() error = %v", err)
	}
	current.Payment.Order = unknownOrder
	expirer, tx, capture := newCapacityHoldExpirerHarness(
		t,
		current,
		processedAt,
		capacityHoldExpirerScenario{},
	)

	expired, err := expirer.ExpireDue(context.Background(), 1)
	if err != nil {
		t.Fatalf("ExpireDue() error = %v", err)
	}
	if len(expired) != 1 ||
		expired[0].Order.PaymentStatus != payment.OrderStatusUnknown ||
		capture.orderUpdates != 0 ||
		capture.holdUpdates != 1 ||
		capture.registrationUpdates != 1 ||
		capture.capacityDecrements != 1 ||
		!tx.committed ||
		tx.rolledBack {
		t.Fatalf("ExpireDue(unknown) = %+v transaction=%+v capture=%+v", expired, tx, capture)
	}
}

func TestCapacityHoldExpirerDoesNotRewriteExistingCancellation(t *testing.T) {
	t.Parallel()

	current, processedAt := dueCapacityHoldFixture(t)
	cancelled, _, err := registration.CancelRegistration(
		current.Registration,
		"user_cancelled",
		current.Registration.CreatedAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("CancelRegistration() error = %v", err)
	}
	current.Registration = cancelled
	expirer, tx, capture := newCapacityHoldExpirerHarness(
		t,
		current,
		processedAt,
		capacityHoldExpirerScenario{},
	)

	expired, err := expirer.ExpireDue(context.Background(), 1)
	if err != nil {
		t.Fatalf("ExpireDue() error = %v", err)
	}
	if len(expired) != 1 ||
		expired[0].Registration.CancellationReason == nil ||
		*expired[0].Registration.CancellationReason != "user_cancelled" ||
		capture.registrationUpdates != 0 ||
		!tx.committed ||
		tx.rolledBack {
		t.Fatalf("ExpireDue(cancelled) = %+v transaction=%+v capture=%+v", expired, tx, capture)
	}
}

func TestCapacityHoldExpirerHandlesEmptyAndRacedCandidate(t *testing.T) {
	t.Parallel()

	current, processedAt := dueCapacityHoldFixture(t)
	expirer, tx, capture := newCapacityHoldExpirerHarness(
		t,
		current,
		processedAt,
		capacityHoldExpirerScenario{candidateMissing: true},
	)
	expired, err := expirer.ExpireDue(context.Background(), 1)
	if err != nil || len(expired) != 0 {
		t.Fatalf("ExpireDue(empty) = %+v, %v", expired, err)
	}
	if !tx.committed || tx.rolledBack || len(capture.lockOrder) != 0 {
		t.Fatalf("empty transaction/capture = %+v %+v", tx, capture)
	}

	current.Payment.Hold.HoldStatus = payment.CapacityHoldStatusExpired
	releasedAt := processedAt
	reason := "other_worker"
	current.Payment.Hold.ReleasedAt = &releasedAt
	current.Payment.Hold.ReleaseReason = &reason
	expirer, tx, capture = newCapacityHoldExpirerHarness(
		t,
		current,
		processedAt,
		capacityHoldExpirerScenario{},
	)
	expired, err = expirer.ExpireDue(context.Background(), 1)
	if err != nil || len(expired) != 0 {
		t.Fatalf("ExpireDue(raced) = %+v, %v", expired, err)
	}
	if !tx.committed || tx.rolledBack ||
		capture.holdUpdates != 0 ||
		capture.capacityDecrements != 0 {
		t.Fatalf("raced transaction/capture = %+v %+v", tx, capture)
	}
}

func TestCapacityHoldExpirerRejectsInvalidBatchWithoutTransaction(t *testing.T) {
	t.Parallel()

	starter := &fakePaidRegistrationTransactionStarter{}
	expirer := &CapacityHoldExpirer{transactions: starter, now: time.Now}
	for _, limit := range []int{0, MaxCapacityHoldExpiryBatch + 1} {
		if _, err := expirer.ExpireDue(context.Background(), limit); !errors.Is(
			err,
			ErrInvalidCapacityHoldExpiryBatch,
		) {
			t.Fatalf("ExpireDue(%d) error = %v", limit, err)
		}
	}
	if starter.begins != 0 {
		t.Fatalf("invalid batches began %d transaction(s)", starter.begins)
	}
}

func TestCapacityHoldExpirerFailsClosedWhenUnconfigured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		expirer *CapacityHoldExpirer
		ctx     context.Context
	}{
		{name: "nil receiver", expirer: nil, ctx: context.Background()},
		{name: "zero value", expirer: &CapacityHoldExpirer{}, ctx: context.Background()},
		{name: "nil context", expirer: &CapacityHoldExpirer{
			transactions: &fakePaidRegistrationTransactionStarter{}, now: time.Now,
		}},
		{name: "nil database constructor", expirer: NewCapacityHoldExpirer(nil), ctx: context.Background()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.expirer.ExpireDue(test.ctx, 1); !errors.Is(
				err,
				ErrInvalidCapacityHoldExpirer,
			) {
				t.Fatalf("ExpireDue() error = %v", err)
			}
		})
	}
}

func TestCapacityHoldExpirerRejectsPaidOrConfirmedActiveContext(t *testing.T) {
	t.Parallel()

	base, processedAt := dueCapacityHoldFixture(t)
	tests := []struct {
		name   string
		mutate func(*PaidRegistrationContext)
	}{
		{
			name: "paid Order",
			mutate: func(current *PaidRegistrationContext) {
				paidOrder, _, err := payment.ConfirmOrderPayment(
					current.Payment.Order,
					payment.PaymentConfirmation{
						ActualPaidCents:     current.Payment.Order.PayableCents,
						WeChatTransactionID: "wx-paid-before-expiry",
						PaidAt:              current.Payment.Order.CreatedAt.Add(time.Minute),
					},
				)
				if err != nil {
					t.Fatalf("ConfirmOrderPayment() error = %v", err)
				}
				current.Payment.Order = paidOrder
			},
		},
		{
			name: "confirmed Registration",
			mutate: func(current *PaidRegistrationContext) {
				confirmed, _, err := registration.ConfirmRegistration(
					current.Registration,
					current.Registration.CreatedAt.Add(time.Minute),
				)
				if err != nil {
					t.Fatalf("ConfirmRegistration() error = %v", err)
				}
				current.Registration = confirmed
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := base
			test.mutate(&current)
			expirer, tx, capture := newCapacityHoldExpirerHarness(
				t,
				current,
				processedAt,
				capacityHoldExpirerScenario{},
			)
			if _, err := expirer.ExpireDue(context.Background(), 1); !errors.Is(
				err,
				ErrCapacityHoldExpiryTransaction,
			) {
				t.Fatalf("ExpireDue() error = %v", err)
			}
			if tx.committed || !tx.rolledBack ||
				capture.orderUpdates != 0 ||
				capture.holdUpdates != 0 {
				t.Fatalf("transaction/capture = %+v %+v", tx, capture)
			}
		})
	}
}

func TestCapacityHoldExpirerRollsBackWriteFailures(t *testing.T) {
	t.Parallel()

	current, processedAt := dueCapacityHoldFixture(t)
	writeFailure := errors.New("write failed")
	tests := []struct {
		name     string
		scenario capacityHoldExpirerScenario
		wantErr  error
	}{
		{
			name:     "Order",
			scenario: capacityHoldExpirerScenario{orderUpdateErr: writeFailure},
			wantErr:  writeFailure,
		},
		{
			name:     "hold",
			scenario: capacityHoldExpirerScenario{holdUpdateErr: writeFailure},
			wantErr:  writeFailure,
		},
		{
			name:     "Registration",
			scenario: capacityHoldExpirerScenario{registrationUpdateErr: writeFailure},
			wantErr:  writeFailure,
		},
		{
			name:     "capacity",
			scenario: capacityHoldExpirerScenario{capacityDecrementErr: sql.ErrNoRows},
			wantErr:  ErrCapacityHoldExpiryTransaction,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expirer, tx, _ := newCapacityHoldExpirerHarness(
				t,
				current,
				processedAt,
				test.scenario,
			)
			if _, err := expirer.ExpireDue(context.Background(), 1); !errors.Is(err, test.wantErr) {
				t.Fatalf("ExpireDue() error = %v, want %v", err, test.wantErr)
			}
			if tx.committed || !tx.rolledBack {
				t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
			}
		})
	}
}

func TestCapacityHoldExpirySerializationIsClassified(t *testing.T) {
	t.Parallel()

	got := classifyCapacityHoldExpiryCommitError(&pgconn.PgError{Code: "40001"})
	if !errors.Is(got, ErrCapacityHoldExpiryTransaction) {
		t.Fatalf("classifyCapacityHoldExpiryCommitError() = %v", got)
	}
}

type capacityHoldExpirerScenario struct {
	candidateMissing      bool
	orderUpdateErr        error
	holdUpdateErr         error
	registrationUpdateErr error
	capacityDecrementErr  error
	commitErr             error
}

type capacityHoldExpirerCapture struct {
	isolation           sql.IsolationLevel
	lockOrder           []string
	orderUpdates        int
	holdUpdates         int
	registrationUpdates int
	capacityDecrements  int
}

func newCapacityHoldExpirerHarness(
	t *testing.T,
	current PaidRegistrationContext,
	processedAt time.Time,
	scenario capacityHoldExpirerScenario,
) (*CapacityHoldExpirer, *fakePaidRegistrationTransaction, *capacityHoldExpirerCapture) {
	t.Helper()

	capture := &capacityHoldExpirerCapture{}
	tx := &fakePaidRegistrationTransaction{commitErr: scenario.commitErr}
	tx.fakeQueryExecutor = &fakeQueryExecutor{queryRow: func(query string, args ...any) rowScanner {
		switch {
		case strings.Contains(query, "FROM xiangwan_capacity_holds AS capacity_hold"):
			if scenario.candidateMissing {
				return &fakeRow{err: sql.ErrNoRows}
			}
			return &fakeRow{values: []any{
				current.Payment.Hold.TenantID,
				current.Payment.Hold.ID,
				current.Payment.Hold.OrderID,
				current.Payment.Hold.RegistrationID,
				current.Payment.Order.SeriesID,
				current.Payment.Order.InstanceID,
				current.Payment.Hold.SessionID,
			}}
		case strings.Contains(query, "FROM xiangwan_activity_series"):
			capture.lockOrder = append(capture.lockOrder, "series")
			return &fakeRow{values: []any{activity.SeriesStatusActive, int64(7)}}
		case strings.Contains(query, "FROM xiangwan_activity_instances"):
			capture.lockOrder = append(capture.lockOrder, "instance")
			return &fakeRow{values: []any{activity.InstanceStatusPublished}}
		case strings.Contains(query, "FROM xiangwan_activity_sessions"):
			capture.lockOrder = append(capture.lockOrder, "session")
			return &fakeRow{values: []any{activity.SessionStatusPublished, int64(9)}}
		case strings.Contains(query, "FROM xiangwan_registrations"):
			capture.lockOrder = append(capture.lockOrder, "registration")
			return &fakeRow{values: paidRegistrationScanValues(current.Registration)}
		case strings.Contains(query, "FROM xiangwan_orders"):
			capture.lockOrder = append(capture.lockOrder, "order")
			return &fakeRow{values: orderScanValues(current.Payment.Order)}
		case strings.Contains(query, "FROM xiangwan_capacity_holds"):
			capture.lockOrder = append(capture.lockOrder, "hold")
			return &fakeRow{values: holdScanValues(current.Payment.Hold)}
		case strings.Contains(query, "UPDATE xiangwan_orders"):
			capture.orderUpdates++
			if scenario.orderUpdateErr != nil {
				return &fakeRow{err: scenario.orderUpdateErr}
			}
			updated := current.Payment.Order
			updated.PaymentStatus = args[2].(payment.OrderStatus)
			updated.ActualPaidCents = args[3].(*int64)
			updated.WeChatTransactionID = args[4].(*string)
			updated.PaidAt = args[5].(*time.Time)
			updated.ClosedAt = args[6].(*time.Time)
			updated.Version++
			updated.UpdatedAt = args[7].(time.Time)
			return &fakeRow{values: orderScanValues(updated)}
		case strings.Contains(query, "UPDATE xiangwan_capacity_holds"):
			capture.holdUpdates++
			if scenario.holdUpdateErr != nil {
				return &fakeRow{err: scenario.holdUpdateErr}
			}
			updated := current.Payment.Hold
			updated.HoldStatus = args[2].(payment.CapacityHoldStatus)
			updated.ConvertedAt = args[3].(*time.Time)
			updated.ReleasedAt = args[4].(*time.Time)
			updated.ReleaseReason = args[5].(*string)
			updated.Version++
			updated.UpdatedAt = args[6].(time.Time)
			return &fakeRow{values: holdScanValues(updated)}
		case strings.Contains(query, "UPDATE xiangwan_registrations"):
			capture.registrationUpdates++
			if scenario.registrationUpdateErr != nil {
				return &fakeRow{err: scenario.registrationUpdateErr}
			}
			updated := current.Registration
			updated.ParticipationStatus = args[2].(registration.ParticipationStatus)
			updated.ConfirmedAt = args[3].(*time.Time)
			updated.CancelledAt = args[4].(*time.Time)
			updated.CancellationReason = args[5].(*string)
			updated.Version++
			updated.UpdatedAt = args[6].(time.Time)
			return &fakeRow{values: paidRegistrationScanValues(updated)}
		case strings.Contains(query, "UPDATE xiangwan_activity_sessions"):
			capture.capacityDecrements++
			if scenario.capacityDecrementErr != nil {
				return &fakeRow{err: scenario.capacityDecrementErr}
			}
			return &fakeRow{values: []any{int64(10)}}
		default:
			t.Fatalf("unexpected query: %s", query)
			return &fakeRow{}
		}
	}}
	starter := &capacityHoldExpiryTransactionStarter{
		tx:      tx,
		capture: capture,
	}
	return &CapacityHoldExpirer{
		transactions: starter,
		now:          func() time.Time { return processedAt },
	}, tx, capture
}

type capacityHoldExpiryTransactionStarter struct {
	tx      paidRegistrationTransaction
	err     error
	capture *capacityHoldExpirerCapture
}

func (starter *capacityHoldExpiryTransactionStarter) beginTx(
	_ context.Context,
	options *sql.TxOptions,
) (paidRegistrationTransaction, error) {
	starter.capture.isolation = options.Isolation
	if starter.err != nil {
		return nil, starter.err
	}
	return starter.tx, nil
}

func dueCapacityHoldFixture(t *testing.T) (PaidRegistrationContext, time.Time) {
	t.Helper()

	startCommand := validPaidRegistrationCommand()
	createdAt := time.Date(2026, time.September, 12, 6, 0, 0, 0, time.UTC)
	current := existingPaidRegistrationContext(t, startCommand, createdAt)
	return current, current.Payment.Hold.ExpiresAt.Add(time.Minute)
}
