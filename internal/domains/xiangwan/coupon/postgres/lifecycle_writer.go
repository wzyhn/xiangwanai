package couponpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidLifecycleCommand = errors.New(
		`invalid xiangwan Coupon lifecycle command`,
	)
	ErrCouponLifecycleConflict = errors.New(
		`xiangwan Coupon lifecycle conflict`,
	)
	ErrCouponOrderUnavailable = errors.New(
		`xiangwan Coupon Order is unavailable`,
	)
)

type HoldForOrderCommand struct {
	TenantID    uuid.UUID
	PrincipalID uuid.UUID
	CouponID    uuid.UUID
	OrderID     uuid.UUID
}

type ReleaseForOrderCommand struct {
	TenantID    uuid.UUID
	PrincipalID uuid.UUID
	CouponID    uuid.UUID
	OrderID     uuid.UUID
	Reason      string
}

type RedeemForOrderCommand struct {
	TenantID    uuid.UUID
	PrincipalID uuid.UUID
	CouponID    uuid.UUID
	OrderID     uuid.UUID
}

type LifecycleResult struct {
	Entry     coupon.Entry
	Duplicate bool
}

// LifecycleWriter is the PostgreSQL-only Coupon/Order consistency boundary.
// The production payment composition may use the same repository inside its
// wider transaction; these methods provide the independently testable writer.
type LifecycleWriter struct {
	transactions lifecycleTransactionStarter
	now          func() time.Time
}

func NewLifecycleWriter(db *sql.DB) *LifecycleWriter {
	return &LifecycleWriter{
		transactions: sqlLifecycleTransactionStarter{db: db},
		now:          time.Now,
	}
}

func (writer *LifecycleWriter) HoldForOrder(
	ctx context.Context,
	command HoldForOrderCommand,
) (LifecycleResult, error) {
	if !validLifecycleIdentity(
		writer,
		command.TenantID,
		command.PrincipalID,
		command.CouponID,
		command.OrderID,
	) {
		return LifecycleResult{}, ErrInvalidLifecycleCommand
	}
	return writer.run(
		ctx,
		command.TenantID,
		command.PrincipalID,
		command.CouponID,
		command.OrderID,
		func(
			ledger Ledger,
			order orderUseFacts,
			now time.Time,
		) (coupon.Entry, error) {
			if order.PaymentStatus != payment.OrderStatusPending &&
				order.PaymentStatus != payment.OrderStatusUnknown ||
				order.CapacityHoldStatus !=
					payment.CapacityHoldStatusActive ||
				!now.Before(order.CapacityHoldExpiresAt) ||
				order.DiscountCents != ledger.Instrument.FaceValueCents {
				return coupon.Entry{}, ErrCouponOrderUnavailable
			}
			return coupon.Hold(coupon.HoldCommand{
				Instrument:         ledger.Instrument,
				History:            ledger.Entries,
				OrderID:            order.ID,
				RegistrationID:     order.RegistrationID,
				SeriesID:           order.SeriesID,
				ActivityType:       order.ActivityType,
				OriginalPriceCents: order.OriginalPriceCents,
				HoldExpiresAt:      order.CapacityHoldExpiresAt,
				At:                 now,
				RecordedAt:         now,
			})
		},
		coupon.EntryTypeHeld,
		`hold:`+command.OrderID.String(),
		nil,
	)
}

func (writer *LifecycleWriter) ReleaseForOrder(
	ctx context.Context,
	command ReleaseForOrderCommand,
) (LifecycleResult, error) {
	reason := strings.TrimSpace(command.Reason)
	if !validLifecycleIdentity(
		writer,
		command.TenantID,
		command.PrincipalID,
		command.CouponID,
		command.OrderID,
	) || reason == `` {
		return LifecycleResult{}, ErrInvalidLifecycleCommand
	}
	return writer.run(
		ctx,
		command.TenantID,
		command.PrincipalID,
		command.CouponID,
		command.OrderID,
		func(
			ledger Ledger,
			order orderUseFacts,
			now time.Time,
		) (coupon.Entry, error) {
			if order.PaymentStatus == payment.OrderStatusPaidConfirmed ||
				(order.CapacityHoldStatus !=
					payment.CapacityHoldStatusReleased &&
					order.CapacityHoldStatus !=
						payment.CapacityHoldStatusExpired) {
				return coupon.Entry{}, ErrCouponOrderUnavailable
			}
			return coupon.Release(coupon.ReleaseCommand{
				Instrument: ledger.Instrument,
				History:    ledger.Entries,
				OrderID:    order.ID,
				Reason:     reason,
				At:         now,
				RecordedAt: now,
			})
		},
		coupon.EntryTypeReleased,
		`release:`+command.OrderID.String(),
		&reason,
	)
}

func (writer *LifecycleWriter) RedeemForOrder(
	ctx context.Context,
	command RedeemForOrderCommand,
) (LifecycleResult, error) {
	if !validLifecycleIdentity(
		writer,
		command.TenantID,
		command.PrincipalID,
		command.CouponID,
		command.OrderID,
	) {
		return LifecycleResult{}, ErrInvalidLifecycleCommand
	}
	return writer.run(
		ctx,
		command.TenantID,
		command.PrincipalID,
		command.CouponID,
		command.OrderID,
		func(
			ledger Ledger,
			order orderUseFacts,
			now time.Time,
		) (coupon.Entry, error) {
			if order.PaymentStatus != payment.OrderStatusPaidConfirmed ||
				order.ParticipationStatus !=
					registration.ParticipationStatusConfirmed ||
				order.PaidAt == nil || order.PaidAt.After(now) {
				return coupon.Entry{}, ErrCouponOrderUnavailable
			}
			return coupon.Redeem(coupon.RedeemCommand{
				Instrument: ledger.Instrument,
				History:    ledger.Entries,
				OrderID:    order.ID,
				At:         now,
				RecordedAt: now,
			})
		},
		coupon.EntryTypeRedeemed,
		`redeem:`+command.OrderID.String(),
		nil,
	)
}

type lifecycleEntryFactory func(
	Ledger,
	orderUseFacts,
	time.Time,
) (coupon.Entry, error)

func (writer *LifecycleWriter) run(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	couponID uuid.UUID,
	orderID uuid.UUID,
	factory lifecycleEntryFactory,
	entryType coupon.EntryType,
	businessKey string,
	expectedReason *string,
) (LifecycleResult, error) {
	tx, err := writer.transactions.beginLifecycleTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return LifecycleResult{}, fmt.Errorf(
			`begin xiangwan Coupon lifecycle: %w`,
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	order, err := tx.lockOrderUseFacts(
		ctx,
		tenantID,
		principalID,
		orderID,
	)
	if err != nil {
		return LifecycleResult{}, err
	}
	if !orderUseFactsMatchCommand(order, tenantID, principalID, orderID) {
		return LifecycleResult{}, ErrCouponLifecycleConflict
	}
	ledger, err := tx.getLedgerForUpdate(
		ctx,
		tenantID,
		principalID,
		couponID,
	)
	if err != nil {
		return LifecycleResult{}, err
	}
	if existing := findLifecycleEntry(
		ledger.Entries,
		entryType,
		businessKey,
	); existing != nil {
		if !lifecycleReplayMatches(*existing, order, expectedReason) {
			return LifecycleResult{}, ErrCouponLifecycleConflict
		}
		if err := tx.Commit(); err != nil {
			return LifecycleResult{}, classifyLifecycleError(err)
		}
		committed = true
		return LifecycleResult{Entry: *existing, Duplicate: true}, nil
	}

	now := writer.now().UTC().Truncate(time.Microsecond)
	candidate, err := factory(ledger, order, now)
	if err != nil {
		return LifecycleResult{}, err
	}
	created, err := tx.appendLifecycleEntry(ctx, candidate)
	if err != nil {
		return LifecycleResult{}, classifyLifecycleError(err)
	}
	if err := tx.Commit(); err != nil {
		return LifecycleResult{}, classifyLifecycleError(err)
	}
	committed = true
	return LifecycleResult{Entry: created}, nil
}

func validLifecycleIdentity(
	writer *LifecycleWriter,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	couponID uuid.UUID,
	orderID uuid.UUID,
) bool {
	return writer != nil && writer.transactions != nil &&
		writer.now != nil && tenantID != uuid.Nil &&
		principalID != uuid.Nil && couponID != uuid.Nil &&
		orderID != uuid.Nil
}

func findLifecycleEntry(
	entries []coupon.Entry,
	entryType coupon.EntryType,
	businessKey string,
) *coupon.Entry {
	for index := range entries {
		if entries[index].EntryType == entryType &&
			entries[index].BusinessKey == businessKey {
			value := entries[index]
			return &value
		}
	}
	return nil
}

func lifecycleReplayMatches(
	entry coupon.Entry,
	order orderUseFacts,
	expectedReason *string,
) bool {
	if coupon.ValidateEntry(entry) != nil || entry.OrderID == nil ||
		entry.RegistrationID == nil || *entry.OrderID != order.ID ||
		*entry.RegistrationID != order.RegistrationID {
		return false
	}
	if expectedReason == nil {
		return entry.Reason == nil
	}
	return entry.Reason != nil && *entry.Reason == *expectedReason
}

type orderUseFacts struct {
	ID                    uuid.UUID
	TenantID              uuid.UUID
	RegistrationID        uuid.UUID
	SeriesID              uuid.UUID
	InstanceID            uuid.UUID
	PrincipalID           uuid.UUID
	ActivityType          activity.ActivityType
	OriginalPriceCents    int64
	DiscountCents         int64
	PaymentStatus         payment.OrderStatus
	PaidAt                *time.Time
	CapacityHoldStatus    payment.CapacityHoldStatus
	CapacityHoldExpiresAt time.Time
	ParticipationStatus   registration.ParticipationStatus
}

func orderUseFactsMatchCommand(
	value orderUseFacts,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	orderID uuid.UUID,
) bool {
	return value.ID == orderID && value.TenantID == tenantID &&
		value.PrincipalID == principalID &&
		value.RegistrationID != uuid.Nil &&
		value.SeriesID != uuid.Nil && value.InstanceID != uuid.Nil &&
		value.OriginalPriceCents > 0 &&
		value.DiscountCents >= 0 &&
		value.CapacityHoldExpiresAt.After(time.Time{})
}

type lifecycleTransactionStarter interface {
	beginLifecycleTx(
		context.Context,
		*sql.TxOptions,
	) (lifecycleTransaction, error)
}

type lifecycleTransaction interface {
	lockOrderUseFacts(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (orderUseFacts, error)
	getLedgerForUpdate(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (Ledger, error)
	appendLifecycleEntry(
		context.Context,
		coupon.Entry,
	) (coupon.Entry, error)
	Commit() error
	Rollback() error
}

func classifyLifecycleError(err error) error {
	if errors.Is(err, ErrGrantExists) {
		return ErrCouponLifecycleConflict
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case `23503`, `23505`, `23514`, `40001`, `40P01`:
			return fmt.Errorf(`%w: %v`, ErrCouponLifecycleConflict, err)
		}
	}
	return err
}
