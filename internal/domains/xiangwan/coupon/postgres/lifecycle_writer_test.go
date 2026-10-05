package couponpostgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

func TestLifecycleWriterHoldsEligibleCouponForOrder(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	ledger := knownCouponLedger(t, now)
	order := knownOrderUseFacts(ledger, now)
	tx := &fakeLifecycleTransaction{ledger: ledger, order: order}
	starter := &fakeLifecycleTransactionStarter{tx: tx}
	writer := &LifecycleWriter{
		transactions: starter,
		now:          func() time.Time { return now },
	}

	result, err := writer.HoldForOrder(
		context.Background(),
		HoldForOrderCommand{
			TenantID:    ledger.Instrument.TenantID,
			PrincipalID: ledger.Instrument.PrincipalID,
			CouponID:    ledger.Instrument.ID,
			OrderID:     order.ID,
		},
	)
	if err != nil {
		t.Fatalf("HoldForOrder() error = %v", err)
	}
	if result.Duplicate || result.Entry.EntryType != coupon.EntryTypeHeld ||
		result.Entry.OrderID == nil || *result.Entry.OrderID != order.ID ||
		tx.appended == nil || !tx.committed || tx.rolledBack ||
		starter.options == nil ||
		starter.options.Isolation != sql.LevelSerializable {
		t.Fatalf(
			"HoldForOrder() = %+v tx=%+v starter=%+v",
			result,
			tx,
			starter,
		)
	}
}

func TestLifecycleWriterRejectsOrderOrCouponMismatch(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	tests := []struct {
		name   string
		mutate func(*orderUseFacts, *Ledger)
		want   error
	}{
		{
			name: "discount is not Coupon face",
			mutate: func(order *orderUseFacts, _ *Ledger) {
				order.DiscountCents--
			},
			want: ErrCouponOrderUnavailable,
		},
		{
			name: "capacity hold is terminal",
			mutate: func(order *orderUseFacts, _ *Ledger) {
				order.CapacityHoldStatus =
					payment.CapacityHoldStatusReleased
			},
			want: ErrCouponOrderUnavailable,
		},
		{
			name: "scope mismatch",
			mutate: func(order *orderUseFacts, _ *Ledger) {
				order.ActivityType = activity.ActivityTypeCourse
			},
			want: coupon.ErrCouponScopeMismatch,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ledger := knownCouponLedger(t, now)
			order := knownOrderUseFacts(ledger, now)
			test.mutate(&order, &ledger)
			tx := &fakeLifecycleTransaction{ledger: ledger, order: order}
			writer := &LifecycleWriter{
				transactions: &fakeLifecycleTransactionStarter{tx: tx},
				now:          func() time.Time { return now },
			}
			_, err := writer.HoldForOrder(
				context.Background(),
				HoldForOrderCommand{
					TenantID:    ledger.Instrument.TenantID,
					PrincipalID: ledger.Instrument.PrincipalID,
					CouponID:    ledger.Instrument.ID,
					OrderID:     order.ID,
				},
			)
			if !errors.Is(err, test.want) || tx.appended != nil ||
				tx.committed || !tx.rolledBack {
				t.Fatalf(
					"HoldForOrder() error=%v tx=%+v",
					err,
					tx,
				)
			}
		})
	}
}

func TestLifecycleWriterReleasesAndRedeemsIdempotently(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	tests := []struct {
		name      string
		prepare   func(*orderUseFacts)
		run       func(*LifecycleWriter, Ledger, orderUseFacts) (LifecycleResult, error)
		entryType coupon.EntryType
		reason    *string
	}{
		{
			name: "release",
			prepare: func(order *orderUseFacts) {
				order.PaymentStatus = payment.OrderStatusClosedUnpaid
				order.CapacityHoldStatus =
					payment.CapacityHoldStatusReleased
			},
			run: func(
				writer *LifecycleWriter,
				ledger Ledger,
				order orderUseFacts,
			) (LifecycleResult, error) {
				return writer.ReleaseForOrder(
					context.Background(),
					ReleaseForOrderCommand{
						TenantID:    ledger.Instrument.TenantID,
						PrincipalID: ledger.Instrument.PrincipalID,
						CouponID:    ledger.Instrument.ID,
						OrderID:     order.ID,
						Reason:      "Order closed unpaid",
					},
				)
			},
			entryType: coupon.EntryTypeReleased,
			reason:    stringPointer("Order closed unpaid"),
		},
		{
			name: "redeem",
			prepare: func(order *orderUseFacts) {
				paidAt := now.Add(-time.Second)
				order.PaymentStatus = payment.OrderStatusPaidConfirmed
				order.ParticipationStatus =
					registration.ParticipationStatusConfirmed
				order.PaidAt = &paidAt
				order.CapacityHoldStatus =
					payment.CapacityHoldStatusConverted
			},
			run: func(
				writer *LifecycleWriter,
				ledger Ledger,
				order orderUseFacts,
			) (LifecycleResult, error) {
				return writer.RedeemForOrder(
					context.Background(),
					RedeemForOrderCommand{
						TenantID:    ledger.Instrument.TenantID,
						PrincipalID: ledger.Instrument.PrincipalID,
						CouponID:    ledger.Instrument.ID,
						OrderID:     order.ID,
					},
				)
			},
			entryType: coupon.EntryTypeRedeemed,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ledger := knownCouponLedger(t, now)
			order := knownOrderUseFacts(ledger, now)
			held := knownHoldEntry(t, ledger, order, now.Add(-time.Minute))
			ledger.Entries = append(ledger.Entries, held)
			test.prepare(&order)
			tx := &fakeLifecycleTransaction{ledger: ledger, order: order}
			writer := &LifecycleWriter{
				transactions: &fakeLifecycleTransactionStarter{tx: tx},
				now:          func() time.Time { return now },
			}
			result, err := test.run(writer, ledger, order)
			if err != nil || result.Duplicate ||
				result.Entry.EntryType != test.entryType ||
				tx.appended == nil || !tx.committed {
				t.Fatalf(
					"%s result=%+v error=%v tx=%+v",
					test.name,
					result,
					err,
					tx,
				)
			}

			replayedLedger := ledger
			replayedLedger.Entries = append(
				replayedLedger.Entries,
				result.Entry,
			)
			replayTx := &fakeLifecycleTransaction{
				ledger: replayedLedger,
				order:  order,
			}
			replayWriter := &LifecycleWriter{
				transactions: &fakeLifecycleTransactionStarter{
					tx: replayTx,
				},
				now: func() time.Time { return now.Add(time.Minute) },
			}
			replayed, err := test.run(
				replayWriter,
				replayedLedger,
				order,
			)
			if err != nil || !replayed.Duplicate ||
				replayed.Entry.ID != result.Entry.ID ||
				replayTx.appended != nil || !replayTx.committed {
				t.Fatalf(
					"%s replay=%+v error=%v tx=%+v",
					test.name,
					replayed,
					err,
					replayTx,
				)
			}
		})
	}
}

func knownCouponLedger(t *testing.T, now time.Time) Ledger {
	t.Helper()
	source := knownInitialGuestSource(t, now.Add(-time.Hour), true)
	grant := knownInitialGrant(t, source, now.Add(-time.Hour))
	return Ledger{
		Instrument: grant.Coupons[0],
		Entries:    []coupon.Entry{grant.Entries[0]},
	}
}

func knownOrderUseFacts(ledger Ledger, now time.Time) orderUseFacts {
	return orderUseFacts{
		ID:                    uuid.New(),
		TenantID:              ledger.Instrument.TenantID,
		RegistrationID:        uuid.New(),
		SeriesID:              uuid.New(),
		InstanceID:            uuid.New(),
		PrincipalID:           ledger.Instrument.PrincipalID,
		ActivityType:          activity.ActivityTypeAIRoundtable,
		OriginalPriceCents:    10000,
		DiscountCents:         ledger.Instrument.FaceValueCents,
		PaymentStatus:         payment.OrderStatusPending,
		CapacityHoldStatus:    payment.CapacityHoldStatusActive,
		CapacityHoldExpiresAt: now.Add(5 * time.Minute),
		ParticipationStatus:   registration.ParticipationStatusPendingPayment,
	}
}

func knownHoldEntry(
	t *testing.T,
	ledger Ledger,
	order orderUseFacts,
	at time.Time,
) coupon.Entry {
	t.Helper()
	entry, err := coupon.Hold(coupon.HoldCommand{
		Instrument:         ledger.Instrument,
		History:            ledger.Entries,
		OrderID:            order.ID,
		RegistrationID:     order.RegistrationID,
		SeriesID:           order.SeriesID,
		ActivityType:       order.ActivityType,
		OriginalPriceCents: order.OriginalPriceCents,
		HoldExpiresAt:      order.CapacityHoldExpiresAt,
		At:                 at,
		RecordedAt:         at,
	})
	if err != nil {
		t.Fatalf("coupon.Hold() error = %v", err)
	}
	return entry
}

func stringPointer(value string) *string {
	return &value
}

type fakeLifecycleTransactionStarter struct {
	tx      lifecycleTransaction
	options *sql.TxOptions
}

func (starter *fakeLifecycleTransactionStarter) beginLifecycleTx(
	_ context.Context,
	options *sql.TxOptions,
) (lifecycleTransaction, error) {
	starter.options = options
	return starter.tx, nil
}

type fakeLifecycleTransaction struct {
	order      orderUseFacts
	orderErr   error
	ledger     Ledger
	ledgerErr  error
	appended   *coupon.Entry
	appendErr  error
	committed  bool
	commitErr  error
	rolledBack bool
}

func (tx *fakeLifecycleTransaction) lockOrderUseFacts(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (orderUseFacts, error) {
	return tx.order, tx.orderErr
}

func (tx *fakeLifecycleTransaction) getLedgerForUpdate(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (Ledger, error) {
	return tx.ledger, tx.ledgerErr
}

func (tx *fakeLifecycleTransaction) appendLifecycleEntry(
	_ context.Context,
	entry coupon.Entry,
) (coupon.Entry, error) {
	tx.appended = &entry
	return entry, tx.appendErr
}

func (tx *fakeLifecycleTransaction) Commit() error {
	tx.committed = true
	return tx.commitErr
}

func (tx *fakeLifecycleTransaction) Rollback() error {
	tx.rolledBack = true
	return nil
}
