// Package payment owns Xiangwan Order payment facts and ten-minute capacity
// holds. Registration, refund, and check-in remain separate state axes.
package payment

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const CapacityHoldDuration = 10 * time.Minute

type OrderStatus string

const (
	OrderStatusPending       OrderStatus = "pending"
	OrderStatusUnknown       OrderStatus = "unknown"
	OrderStatusPaidConfirmed OrderStatus = "paid_confirmed"
	OrderStatusSettledZero   OrderStatus = "settled_zero"
	OrderStatusClosedUnpaid  OrderStatus = "closed_unpaid"
)

type CapacityHoldStatus string

const (
	CapacityHoldStatusActive    CapacityHoldStatus = "active"
	CapacityHoldStatusConverted CapacityHoldStatus = "converted"
	CapacityHoldStatusReleased  CapacityHoldStatus = "released"
	CapacityHoldStatusExpired   CapacityHoldStatus = "expired"
)

type Order struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	RegistrationID    uuid.UUID
	SeriesID          uuid.UUID
	InstanceID        uuid.UUID
	SessionID         uuid.UUID
	PrincipalID       uuid.UUID
	PaymentStatus     OrderStatus
	IdempotencyKey    string
	MerchantOrderNo   string
	PaymentAppID      string
	PaymentMerchantID string
	// MerchantConfigGenerationID identifies the credential/configuration
	// generation that created this order. It is optional for legacy orders;
	// paid runtime writes must provide it explicitly.
	MerchantConfigGenerationID uuid.UUID
	OriginalPriceCents         int64
	DiscountCents              int64
	PayableCents               int64
	ActualPaidCents            *int64
	WeChatTransactionID        *string
	PaidAt                     *time.Time
	ClosedAt                   *time.Time
	Version                    int64
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
}

type CapacityHold struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	OrderID        uuid.UUID
	RegistrationID uuid.UUID
	SessionID      uuid.UUID
	HoldStatus     CapacityHoldStatus
	ExpiresAt      time.Time
	ConvertedAt    *time.Time
	ReleasedAt     *time.Time
	ReleaseReason  *string
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type PaymentContext struct {
	Order Order
	Hold  CapacityHold
}

type NewPaymentContextCommand struct {
	TenantID                   uuid.UUID
	RegistrationID             uuid.UUID
	SeriesID                   uuid.UUID
	InstanceID                 uuid.UUID
	SessionID                  uuid.UUID
	PrincipalID                uuid.UUID
	IdempotencyKey             string
	MerchantOrderNo            string
	PaymentAppID               string
	PaymentMerchantID          string
	MerchantConfigGenerationID uuid.UUID
	OriginalPriceCents         int64
	DiscountCents              int64
	Now                        time.Time
}

type PaymentConfirmation struct {
	ActualPaidCents     int64
	WeChatTransactionID string
	PaidAt              time.Time
}

var (
	ErrInvalidPaymentContext      = errors.New("invalid xiangwan payment context")
	ErrOrderTerminal              = errors.New("xiangwan Order is terminal")
	ErrPaymentConfirmation        = errors.New("xiangwan payment confirmation conflicts with recorded fact")
	ErrCapacityHoldTerminal       = errors.New("xiangwan capacity hold is terminal")
	ErrCapacityHoldReplayConflict = errors.New("xiangwan capacity hold replay conflicts with recorded fact")
)

var paymentIdempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// NewPaymentContext creates immutable price/payment identity snapshots and an
// active ten-minute hold. The caller must persist both in the same PostgreSQL
// Session-lock transaction that reserves capacity.
func NewPaymentContext(command NewPaymentContextCommand) (PaymentContext, error) {
	if err := validateNewPaymentContextCommand(command); err != nil {
		return PaymentContext{}, err
	}

	now := command.Now.UTC()
	order := Order{
		ID:                         uuid.New(),
		TenantID:                   command.TenantID,
		RegistrationID:             command.RegistrationID,
		SeriesID:                   command.SeriesID,
		InstanceID:                 command.InstanceID,
		SessionID:                  command.SessionID,
		PrincipalID:                command.PrincipalID,
		PaymentStatus:              OrderStatusPending,
		IdempotencyKey:             command.IdempotencyKey,
		MerchantOrderNo:            command.MerchantOrderNo,
		PaymentAppID:               command.PaymentAppID,
		PaymentMerchantID:          command.PaymentMerchantID,
		MerchantConfigGenerationID: command.MerchantConfigGenerationID,
		OriginalPriceCents:         command.OriginalPriceCents,
		DiscountCents:              command.DiscountCents,
		PayableCents:               command.OriginalPriceCents - command.DiscountCents,
		Version:                    1,
		CreatedAt:                  now,
		UpdatedAt:                  now,
	}
	hold := CapacityHold{
		ID:             uuid.New(),
		TenantID:       command.TenantID,
		OrderID:        order.ID,
		RegistrationID: command.RegistrationID,
		SessionID:      command.SessionID,
		HoldStatus:     CapacityHoldStatusActive,
		ExpiresAt:      now.Add(CapacityHoldDuration),
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return PaymentContext{Order: order, Hold: hold}, nil
}

// MarkOrderUnknown records that the provider outcome must be queried before
// the order can be considered unpaid.
func MarkOrderUnknown(current Order, at time.Time) (Order, bool, error) {
	if err := validateOrderTransitionTime(current, at); err != nil {
		return Order{}, false, err
	}
	switch current.PaymentStatus {
	case OrderStatusUnknown:
		return cloneOrder(current), false, nil
	case OrderStatusPending:
	case OrderStatusPaidConfirmed, OrderStatusSettledZero, OrderStatusClosedUnpaid:
		return Order{}, false, ErrOrderTerminal
	default:
		return Order{}, false, fmt.Errorf("%w: unknown Order status", ErrInvalidPaymentContext)
	}

	updated := cloneOrder(current)
	updated.PaymentStatus = OrderStatusUnknown
	updated.Version++
	updated.UpdatedAt = at.UTC()
	return updated, true, nil
}

// CloseOrderUnpaid closes a pending/unknown Order after authoritative provider
// reconciliation. A later trusted payment notice may still reveal a late
// payment; that transition is accepted so the application can issue a refund.
func CloseOrderUnpaid(current Order, at time.Time) (Order, bool, error) {
	if err := validateOrderTransitionTime(current, at); err != nil {
		return Order{}, false, err
	}
	switch current.PaymentStatus {
	case OrderStatusClosedUnpaid:
		return cloneOrder(current), false, nil
	case OrderStatusPending, OrderStatusUnknown:
	case OrderStatusPaidConfirmed, OrderStatusSettledZero:
		return Order{}, false, ErrOrderTerminal
	default:
		return Order{}, false, fmt.Errorf("%w: unknown Order status", ErrInvalidPaymentContext)
	}

	updated := cloneOrder(current)
	closedAt := at.UTC()
	updated.PaymentStatus = OrderStatusClosedUnpaid
	updated.ClosedAt = &closedAt
	updated.Version++
	updated.UpdatedAt = closedAt
	return updated, true, nil
}

// SettleOrderZero records a locally-settled Order whose immutable discount
// covers the complete price snapshot. It deliberately records no provider
// payment fact. The caller must confirm Registration, convert capacity, and
// redeem the selected Coupon in the same PostgreSQL transaction.
func SettleOrderZero(current Order, at time.Time) (Order, bool, error) {
	if err := validateOrderTransitionTime(current, at); err != nil {
		return Order{}, false, err
	}
	if current.PayableCents != 0 ||
		current.DiscountCents != current.OriginalPriceCents ||
		current.ActualPaidCents != nil || current.WeChatTransactionID != nil ||
		current.PaidAt != nil || current.ClosedAt != nil {
		return Order{}, false, fmt.Errorf(
			"%w: zero settlement requires an exact local discount",
			ErrInvalidPaymentContext,
		)
	}
	switch current.PaymentStatus {
	case OrderStatusSettledZero:
		return cloneOrder(current), false, nil
	case OrderStatusPending:
	case OrderStatusUnknown, OrderStatusPaidConfirmed, OrderStatusClosedUnpaid:
		return Order{}, false, ErrOrderTerminal
	default:
		return Order{}, false, fmt.Errorf("%w: unknown Order status", ErrInvalidPaymentContext)
	}

	updated := cloneOrder(current)
	updated.PaymentStatus = OrderStatusSettledZero
	updated.Version++
	updated.UpdatedAt = at.UTC()
	return updated, true, nil
}

// ConfirmOrderPayment accepts only a trusted provider payment fact whose amount
// exactly matches the immutable payable snapshot. Exact retries are idempotent.
func ConfirmOrderPayment(
	current Order,
	confirmation PaymentConfirmation,
) (Order, bool, error) {
	if err := validatePaymentConfirmation(current, confirmation); err != nil {
		return Order{}, false, err
	}
	if current.PaymentStatus == OrderStatusPaidConfirmed {
		if current.ActualPaidCents == nil ||
			current.WeChatTransactionID == nil ||
			current.PaidAt == nil {
			return Order{}, false, fmt.Errorf("%w: recorded payment fact is incomplete", ErrInvalidPaymentContext)
		}
		if *current.ActualPaidCents != confirmation.ActualPaidCents ||
			*current.WeChatTransactionID != confirmation.WeChatTransactionID ||
			!current.PaidAt.Equal(confirmation.PaidAt) {
			return Order{}, false, ErrPaymentConfirmation
		}
		return cloneOrder(current), false, nil
	}
	switch current.PaymentStatus {
	case OrderStatusPending, OrderStatusUnknown, OrderStatusClosedUnpaid:
	case OrderStatusSettledZero:
		return Order{}, false, ErrOrderTerminal
	default:
		return Order{}, false, fmt.Errorf("%w: unknown Order status", ErrInvalidPaymentContext)
	}

	updated := cloneOrder(current)
	paidAt := confirmation.PaidAt.UTC()
	actualPaidCents := confirmation.ActualPaidCents
	transactionID := confirmation.WeChatTransactionID
	updated.PaymentStatus = OrderStatusPaidConfirmed
	updated.ActualPaidCents = &actualPaidCents
	updated.WeChatTransactionID = &transactionID
	updated.PaidAt = &paidAt
	updated.Version++
	updated.UpdatedAt = paidAt
	if updated.UpdatedAt.Before(current.UpdatedAt) {
		updated.UpdatedAt = current.UpdatedAt
	}
	return updated, true, nil
}

// ConvertCapacityHold consumes an active hold while it is still valid.
func ConvertCapacityHold(current CapacityHold, at time.Time) (CapacityHold, bool, error) {
	if err := validateHoldTransitionTime(current, at); err != nil {
		return CapacityHold{}, false, err
	}
	switch current.HoldStatus {
	case CapacityHoldStatusConverted:
		return cloneCapacityHold(current), false, nil
	case CapacityHoldStatusActive:
	case CapacityHoldStatusReleased, CapacityHoldStatusExpired:
		return CapacityHold{}, false, ErrCapacityHoldTerminal
	default:
		return CapacityHold{}, false, fmt.Errorf("%w: unknown hold status", ErrInvalidPaymentContext)
	}
	if !at.Before(current.ExpiresAt) {
		return CapacityHold{}, false, ErrCapacityHoldTerminal
	}

	updated := cloneCapacityHold(current)
	convertedAt := at.UTC()
	updated.HoldStatus = CapacityHoldStatusConverted
	updated.ConvertedAt = &convertedAt
	updated.Version++
	updated.UpdatedAt = convertedAt
	return updated, true, nil
}

// ReleaseCapacityHold releases an active hold before payment confirmation.
func ReleaseCapacityHold(
	current CapacityHold,
	reason string,
	at time.Time,
) (CapacityHold, bool, error) {
	if !at.Before(current.ExpiresAt) {
		return CapacityHold{}, false, fmt.Errorf("%w: hold has expired", ErrInvalidPaymentContext)
	}
	return finishCapacityHold(current, CapacityHoldStatusReleased, reason, at)
}

// ExpireCapacityHold records release after the half-open expiry boundary.
func ExpireCapacityHold(
	current CapacityHold,
	reason string,
	at time.Time,
) (CapacityHold, bool, error) {
	if at.Before(current.ExpiresAt) {
		return CapacityHold{}, false, fmt.Errorf("%w: hold has not expired", ErrInvalidPaymentContext)
	}
	return finishCapacityHold(current, CapacityHoldStatusExpired, reason, at)
}

func finishCapacityHold(
	current CapacityHold,
	status CapacityHoldStatus,
	reason string,
	at time.Time,
) (CapacityHold, bool, error) {
	normalizedReason := strings.TrimSpace(reason)
	if normalizedReason == "" || len([]rune(normalizedReason)) > 128 {
		return CapacityHold{}, false, fmt.Errorf("%w: invalid release reason", ErrInvalidPaymentContext)
	}
	if err := validateHoldTransitionTime(current, at); err != nil {
		return CapacityHold{}, false, err
	}
	switch current.HoldStatus {
	case status:
		if current.ReleasedAt == nil || current.ReleaseReason == nil {
			return CapacityHold{}, false, fmt.Errorf("%w: recorded release fact is incomplete", ErrInvalidPaymentContext)
		}
		if *current.ReleaseReason != normalizedReason || !current.ReleasedAt.Equal(at) {
			return CapacityHold{}, false, ErrCapacityHoldReplayConflict
		}
		return cloneCapacityHold(current), false, nil
	case CapacityHoldStatusActive:
	case CapacityHoldStatusConverted, CapacityHoldStatusReleased, CapacityHoldStatusExpired:
		return CapacityHold{}, false, ErrCapacityHoldTerminal
	default:
		return CapacityHold{}, false, fmt.Errorf("%w: unknown hold status", ErrInvalidPaymentContext)
	}

	updated := cloneCapacityHold(current)
	releasedAt := at.UTC()
	updated.HoldStatus = status
	updated.ReleasedAt = &releasedAt
	updated.ReleaseReason = &normalizedReason
	updated.Version++
	updated.UpdatedAt = releasedAt
	return updated, true, nil
}

func validateNewPaymentContextCommand(command NewPaymentContextCommand) error {
	switch {
	case command.TenantID == uuid.Nil:
		return fmt.Errorf("%w: tenant_id is required", ErrInvalidPaymentContext)
	case command.RegistrationID == uuid.Nil:
		return fmt.Errorf("%w: registration_id is required", ErrInvalidPaymentContext)
	case command.SeriesID == uuid.Nil:
		return fmt.Errorf("%w: series_id is required", ErrInvalidPaymentContext)
	case command.InstanceID == uuid.Nil:
		return fmt.Errorf("%w: instance_id is required", ErrInvalidPaymentContext)
	case command.SessionID == uuid.Nil:
		return fmt.Errorf("%w: session_id is required", ErrInvalidPaymentContext)
	case command.PrincipalID == uuid.Nil:
		return fmt.Errorf("%w: principal_id is required", ErrInvalidPaymentContext)
	case !paymentIdempotencyKeyPattern.MatchString(command.IdempotencyKey):
		return fmt.Errorf("%w: idempotency_key is invalid", ErrInvalidPaymentContext)
	case invalidBoundedText(command.MerchantOrderNo, 64):
		return fmt.Errorf("%w: merchant_order_no is invalid", ErrInvalidPaymentContext)
	case invalidBoundedText(command.PaymentAppID, 64):
		return fmt.Errorf("%w: payment_app_id is invalid", ErrInvalidPaymentContext)
	case invalidBoundedText(command.PaymentMerchantID, 64):
		return fmt.Errorf("%w: payment_merchant_id is invalid", ErrInvalidPaymentContext)
	case command.OriginalPriceCents <= 0:
		return fmt.Errorf("%w: original_price_cents must be positive", ErrInvalidPaymentContext)
	case command.DiscountCents < 0 || command.DiscountCents > command.OriginalPriceCents:
		return fmt.Errorf("%w: discount_cents exceeds price", ErrInvalidPaymentContext)
	case command.Now.IsZero():
		return fmt.Errorf("%w: now is required", ErrInvalidPaymentContext)
	default:
		return nil
	}
}

func validatePaymentConfirmation(current Order, confirmation PaymentConfirmation) error {
	switch {
	case current.CreatedAt.IsZero() || current.PayableCents <= 0:
		return fmt.Errorf("%w: invalid Order snapshot", ErrInvalidPaymentContext)
	case confirmation.ActualPaidCents != current.PayableCents:
		return fmt.Errorf("%w: paid amount does not match payable snapshot", ErrPaymentConfirmation)
	case invalidBoundedText(confirmation.WeChatTransactionID, 128):
		return fmt.Errorf("%w: transaction ID is invalid", ErrInvalidPaymentContext)
	case confirmation.PaidAt.IsZero() || confirmation.PaidAt.Before(current.CreatedAt):
		return fmt.Errorf("%w: paid_at is invalid", ErrInvalidPaymentContext)
	default:
		return nil
	}
}

func validateOrderTransitionTime(current Order, at time.Time) error {
	if current.CreatedAt.IsZero() || at.IsZero() || at.Before(current.CreatedAt) ||
		(!current.UpdatedAt.IsZero() && at.Before(current.UpdatedAt)) {
		return fmt.Errorf("%w: invalid Order transition time", ErrInvalidPaymentContext)
	}
	return nil
}

func validateHoldTransitionTime(current CapacityHold, at time.Time) error {
	if current.CreatedAt.IsZero() || current.ExpiresAt.IsZero() ||
		current.ExpiresAt.Sub(current.CreatedAt) != CapacityHoldDuration ||
		at.IsZero() || at.Before(current.CreatedAt) ||
		(!current.UpdatedAt.IsZero() && at.Before(current.UpdatedAt)) {
		return fmt.Errorf("%w: invalid hold transition time", ErrInvalidPaymentContext)
	}
	return nil
}

func invalidBoundedText(value string, maxRunes int) bool {
	return value == "" ||
		value != strings.TrimSpace(value) ||
		len([]rune(value)) > maxRunes
}

func cloneOrder(value Order) Order {
	cloned := value
	cloned.ActualPaidCents = cloneInt64(value.ActualPaidCents)
	cloned.WeChatTransactionID = cloneString(value.WeChatTransactionID)
	cloned.PaidAt = cloneTime(value.PaidAt)
	cloned.ClosedAt = cloneTime(value.ClosedAt)
	return cloned
}

func cloneCapacityHold(value CapacityHold) CapacityHold {
	cloned := value
	cloned.ConvertedAt = cloneTime(value.ConvertedAt)
	cloned.ReleasedAt = cloneTime(value.ReleasedAt)
	cloned.ReleaseReason = cloneString(value.ReleaseReason)
	return cloned
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
