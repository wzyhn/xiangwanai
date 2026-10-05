package paymentpostgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/google/uuid"
)

var (
	ErrCouponPaymentUnavailable = errors.New(`xiangwan Coupon is unavailable for payment`)
	ErrCouponPaymentConflict    = errors.New(`xiangwan Coupon payment facts conflict`)
)

type couponOrderRepository interface {
	GetLedgerForUpdate(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (couponpostgres.Ledger, error)
	GetLedgerByOrderForUpdate(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (couponpostgres.Ledger, error)
	AppendLifecycleEntry(
		context.Context,
		coupon.Entry,
	) (coupon.Entry, error)
}

type AppliedCoupon struct {
	Instrument coupon.Coupon
	Entry      coupon.Entry
}

func (tx *sqlPaidRegistrationTransaction) couponOrderRepository() couponOrderRepository {
	return couponpostgres.NewRepository(tx.tx)
}

func lockSelectedCoupon(
	ctx context.Context,
	repository couponOrderRepository,
	tenantID uuid.UUID,
	couponID uuid.UUID,
	principalID uuid.UUID,
) (*couponpostgres.Ledger, error) {
	if repository == nil {
		return nil, ErrCouponPaymentUnavailable
	}
	ledger, err := repository.GetLedgerForUpdate(
		ctx,
		tenantID,
		principalID,
		couponID,
	)
	if err != nil {
		return nil, classifyCouponPaymentReadError(err)
	}
	return &ledger, nil
}

func appendOrderCouponHold(
	ctx context.Context,
	repository couponOrderRepository,
	ledger *couponpostgres.Ledger,
	orderID uuid.UUID,
	registrationID uuid.UUID,
	seriesID uuid.UUID,
	activityType activity.ActivityType,
	originalPriceCents int64,
	holdExpiresAt time.Time,
	now time.Time,
) (coupon.Entry, error) {
	if repository == nil || ledger == nil {
		return coupon.Entry{}, ErrCouponPaymentUnavailable
	}
	if ledger.Instrument.FaceValueCents > originalPriceCents {
		return coupon.Entry{}, ErrCouponPaymentUnavailable
	}
	entry, err := coupon.Hold(coupon.HoldCommand{
		Instrument:         ledger.Instrument,
		History:            ledger.Entries,
		OrderID:            orderID,
		RegistrationID:     registrationID,
		SeriesID:           seriesID,
		ActivityType:       activityType,
		OriginalPriceCents: originalPriceCents,
		HoldExpiresAt:      holdExpiresAt,
		At:                 now,
		RecordedAt:         now,
	})
	if err != nil {
		return coupon.Entry{},
			fmt.Errorf(`%w: %v`, ErrCouponPaymentUnavailable, err)
	}
	entry, err = repository.AppendLifecycleEntry(ctx, entry)
	if err != nil {
		return coupon.Entry{},
			classifyCouponPaymentWriteError(err)
	}
	ledger.Entries = append(ledger.Entries, entry)
	return entry, nil
}

func lockOrderCoupon(
	ctx context.Context,
	repository couponOrderRepository,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (*couponpostgres.Ledger, error) {
	if repository == nil {
		return nil, ErrCouponPaymentConflict
	}
	ledger, err := repository.GetLedgerByOrderForUpdate(
		ctx,
		tenantID,
		orderID,
	)
	if errors.Is(err, couponpostgres.ErrCouponNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, classifyCouponPaymentReadError(err)
	}
	return &ledger, nil
}

func redeemOrderCoupon(
	ctx context.Context,
	repository couponOrderRepository,
	ledger *couponpostgres.Ledger,
	orderID uuid.UUID,
	now time.Time,
) (*coupon.Entry, error) {
	if ledger == nil {
		return nil, nil
	}
	if existing := matchingCouponEntry(
		ledger.Entries,
		coupon.EntryTypeRedeemed,
		orderID,
	); existing != nil {
		return existing, nil
	}
	entry, err := coupon.Redeem(coupon.RedeemCommand{
		Instrument: ledger.Instrument,
		History:    ledger.Entries,
		OrderID:    orderID,
		At:         now,
		RecordedAt: now,
	})
	if err != nil {
		return nil, fmt.Errorf(`%w: redeem: %v`, ErrCouponPaymentConflict, err)
	}
	entry, err = repository.AppendLifecycleEntry(ctx, entry)
	if err != nil {
		return nil, classifyCouponPaymentWriteError(err)
	}
	ledger.Entries = append(ledger.Entries, entry)
	return &entry, nil
}

func releaseOrderCoupon(
	ctx context.Context,
	repository couponOrderRepository,
	ledger *couponpostgres.Ledger,
	orderID uuid.UUID,
	reason string,
	now time.Time,
) (*coupon.Entry, error) {
	if ledger == nil {
		return nil, nil
	}
	if existing := matchingCouponEntry(
		ledger.Entries,
		coupon.EntryTypeReleased,
		orderID,
	); existing != nil {
		return existing, nil
	}
	entry, err := coupon.Release(coupon.ReleaseCommand{
		Instrument: ledger.Instrument,
		History:    ledger.Entries,
		OrderID:    orderID,
		Reason:     reason,
		At:         now,
		RecordedAt: now,
	})
	if err != nil {
		return nil, fmt.Errorf(`%w: release: %v`, ErrCouponPaymentConflict, err)
	}
	entry, err = repository.AppendLifecycleEntry(ctx, entry)
	if err != nil {
		return nil, classifyCouponPaymentWriteError(err)
	}
	ledger.Entries = append(ledger.Entries, entry)
	return &entry, nil
}

func resolveRefundRequiredOrderCoupon(
	ctx context.Context,
	repository couponOrderRepository,
	ledger *couponpostgres.Ledger,
	orderID uuid.UUID,
	now time.Time,
) (*coupon.Entry, error) {
	if ledger == nil {
		return nil, nil
	}
	if redeemed := matchingCouponEntry(
		ledger.Entries,
		coupon.EntryTypeRedeemed,
		orderID,
	); redeemed != nil {
		return redeemed, nil
	}
	return releaseOrderCoupon(
		ctx,
		repository,
		ledger,
		orderID,
		"payment_refund_required",
		now,
	)
}

func matchingCouponEntry(
	entries []coupon.Entry,
	entryType coupon.EntryType,
	orderID uuid.UUID,
) *coupon.Entry {
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.EntryType == entryType && entry.OrderID != nil &&
			*entry.OrderID == orderID {
			cloned := entry
			return &cloned
		}
	}
	return nil
}

func latestOrderCouponEntry(
	entries []coupon.Entry,
	orderID uuid.UUID,
) *coupon.Entry {
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.OrderID != nil && *entry.OrderID == orderID {
			cloned := entry
			return &cloned
		}
	}
	return nil
}

func appliedCoupon(
	ledger *couponpostgres.Ledger,
	entry *coupon.Entry,
) *AppliedCoupon {
	if ledger == nil || entry == nil {
		return nil
	}
	return &AppliedCoupon{
		Instrument: ledger.Instrument,
		Entry:      *entry,
	}
}

func classifyCouponPaymentReadError(err error) error {
	if errors.Is(err, couponpostgres.ErrCouponNotFound) ||
		errors.Is(err, coupon.ErrInvalidLedger) ||
		errors.Is(err, coupon.ErrInvalidCoupon) {
		return ErrCouponPaymentUnavailable
	}
	return err
}

func classifyCouponPaymentWriteError(err error) error {
	if errors.Is(err, couponpostgres.ErrGrantExists) ||
		errors.Is(err, coupon.ErrInvalidEntry) {
		return ErrCouponPaymentConflict
	}
	return err
}
