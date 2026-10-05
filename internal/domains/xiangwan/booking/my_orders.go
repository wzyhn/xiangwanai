package booking

import (
	"errors"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

const (
	DefaultMyOrdersLimit = 20
	MaxMyOrdersLimit     = 100
	MaxMyOrdersCursorAge = 30 * time.Minute
	MyOrdersFutureSkew   = 5 * time.Second
	MyOrdersCurrency     = "CNY"
)

type MyOrderState string

const (
	MyOrderStateAll              MyOrderState = "all"
	MyOrderStatePendingPayment   MyOrderState = "pending_payment"
	MyOrderStateRefundProcessing MyOrderState = "refund_processing"
	MyOrderStatePaid             MyOrderState = "paid"
	MyOrderStateRefunded         MyOrderState = "refunded"
	MyOrderStateClosed           MyOrderState = "closed"
)

type MyOrderOutcome string

const (
	MyOrderOutcomePendingPayment      MyOrderOutcome = "pending_payment"
	MyOrderOutcomePaymentConfirming   MyOrderOutcome = "payment_confirming"
	MyOrderOutcomePaidConfirmed       MyOrderOutcome = "paid_confirmed"
	MyOrderOutcomeSettledZero         MyOrderOutcome = "settled_zero"
	MyOrderOutcomeClosedUnpaid        MyOrderOutcome = "closed_unpaid"
	MyOrderOutcomeRefundPendingManual MyOrderOutcome = "refund_pending_manual"
	MyOrderOutcomeRefundProcessing    MyOrderOutcome = "refund_processing"
	MyOrderOutcomeRefundFailed        MyOrderOutcome = "refund_failed"
	MyOrderOutcomeRefundRejected      MyOrderOutcome = "refund_rejected"
	MyOrderOutcomeRefunded            MyOrderOutcome = "refunded"
)

type MyOrderFilter struct {
	TenantID    uuid.UUID
	PrincipalID uuid.UUID
	State       MyOrderState
	Limit       int
	Cursor      string
	At          time.Time
}

type MyOrderItem struct {
	OrderID                    uuid.UUID
	OrderVersion               int64
	RegistrationID             uuid.UUID
	RegistrationVersion        int64
	SeriesID                   uuid.UUID
	SeriesTitle                string
	InstanceID                 uuid.UUID
	InstanceTitle              string
	SessionID                  uuid.UUID
	SessionTitle               string
	SessionStartAt             time.Time
	SessionEndAt               time.Time
	ParticipationStatus        registration.ParticipationStatus
	ReservationState           MyRegistrationState
	ReservationHasActiveAccess bool
	PaymentStatus              payment.OrderStatus
	PaymentConfirmationPending bool
	OriginalPriceCents         int64
	DiscountCents              int64
	PayableCents               int64
	ActualPaidCents            *int64
	Currency                   string
	PaidAt                     *time.Time
	ClosedAt                   *time.Time
	HoldStatus                 payment.CapacityHoldStatus
	HoldExpiresAt              time.Time
	HoldVersion                int64
	CanContinuePayment         bool
	Refund                     *MyRegistrationRefundSummary
	State                      MyOrderState
	Outcome                    MyOrderOutcome
	LastBusinessAt             time.Time
	SortRank                   int
	SortAt                     time.Time
}

type MyOrdersPage struct {
	Items       []MyOrderItem
	ActiveState MyOrderState
	AsOf        time.Time
	NextCursor  string
}

type MyOrderDetail struct {
	Item MyOrderItem
	AsOf time.Time
}

var ErrInvalidMyOrderFacts = errors.New("invalid xiangwan My Order facts")

var (
	ErrInvalidMyOrdersFilter = errors.New(
		"invalid xiangwan My Orders filter",
	)
	ErrInvalidMyOrdersCursor = errors.New(
		"invalid xiangwan My Orders cursor",
	)
	ErrStaleMyOrdersCursor = errors.New(
		"stale xiangwan My Orders cursor",
	)
	ErrInvalidMyOrderIdentity = errors.New(
		"invalid xiangwan My Order identity",
	)
	ErrMyOrderNotFound = errors.New(
		"xiangwan My Order not found",
	)
)

func ProjectMyOrder(
	reservation MyRegistrationItem,
) (MyOrderItem, error) {
	if !validMyOrderReservation(reservation) {
		return MyOrderItem{}, ErrInvalidMyOrderFacts
	}
	order := reservation.Order
	state := classifyMyOrder(*order, reservation.Refund)
	lastBusinessAt := latestTime(
		order.UpdatedAt,
		order.HoldUpdatedAt,
	)
	if reservation.Refund != nil {
		lastBusinessAt = latestTime(
			lastBusinessAt,
			reservation.Refund.UpdatedAt,
		)
	}
	return MyOrderItem{
		OrderID:                    order.OrderID,
		OrderVersion:               order.Version,
		RegistrationID:             reservation.RegistrationID,
		RegistrationVersion:        reservation.RegistrationVersion,
		SeriesID:                   reservation.SeriesID,
		SeriesTitle:                reservation.SeriesTitle,
		InstanceID:                 reservation.InstanceID,
		InstanceTitle:              reservation.InstanceTitle,
		SessionID:                  reservation.SessionID,
		SessionTitle:               reservation.SessionTitle,
		SessionStartAt:             reservation.SessionStartAt.UTC(),
		SessionEndAt:               reservation.SessionEndAt.UTC(),
		ParticipationStatus:        reservation.ParticipationStatus,
		ReservationState:           reservation.State,
		ReservationHasActiveAccess: reservation.HasActiveAccess,
		PaymentStatus:              order.PaymentStatus,
		PaymentConfirmationPending: order.PaymentStatus ==
			payment.OrderStatusUnknown,
		OriginalPriceCents: order.OriginalPriceCents,
		DiscountCents:      order.DiscountCents,
		PayableCents:       order.PayableCents,
		ActualPaidCents:    cloneInt64(order.ActualPaidCents),
		Currency:           MyOrdersCurrency,
		PaidAt:             cloneTime(order.PaidAt),
		ClosedAt:           cloneTime(order.ClosedAt),
		HoldStatus:         order.HoldStatus,
		HoldExpiresAt:      order.HoldExpiresAt.UTC(),
		HoldVersion:        order.HoldVersion,
		CanContinuePayment: reservation.CanContinuePayment,
		Refund:             cloneMyRegistrationRefund(reservation.Refund),
		State:              state,
		Outcome:            classifyMyOrderOutcome(*order, reservation.Refund),
		LastBusinessAt:     lastBusinessAt.UTC(),
		SortRank:           MyOrderStateSortRank(state),
		SortAt:             lastBusinessAt.UTC(),
	}, nil
}

func ValidMyOrderState(value MyOrderState) bool {
	switch value {
	case MyOrderStateAll,
		MyOrderStatePendingPayment,
		MyOrderStateRefundProcessing,
		MyOrderStatePaid,
		MyOrderStateRefunded,
		MyOrderStateClosed:
		return true
	default:
		return false
	}
}

func MyOrderStateSortRank(value MyOrderState) int {
	switch value {
	case MyOrderStatePendingPayment:
		return 1
	case MyOrderStateRefundProcessing:
		return 2
	case MyOrderStatePaid:
		return 3
	case MyOrderStateRefunded:
		return 4
	case MyOrderStateClosed:
		return 5
	default:
		return 0
	}
}

func classifyMyOrder(
	order MyRegistrationOrderSummary,
	refundSummary *MyRegistrationRefundSummary,
) MyOrderState {
	if refundSummary != nil {
		switch refundSummary.RefundStatus {
		case refund.StatusRefunded:
			return MyOrderStateRefunded
		case refund.StatusPendingManual,
			refund.StatusProcessing,
			refund.StatusFailed:
			return MyOrderStateRefundProcessing
		}
	}
	switch order.PaymentStatus {
	case payment.OrderStatusPaidConfirmed,
		payment.OrderStatusSettledZero:
		return MyOrderStatePaid
	case payment.OrderStatusClosedUnpaid:
		return MyOrderStateClosed
	default:
		return MyOrderStatePendingPayment
	}
}

func classifyMyOrderOutcome(
	order MyRegistrationOrderSummary,
	refundSummary *MyRegistrationRefundSummary,
) MyOrderOutcome {
	if refundSummary != nil {
		switch refundSummary.RefundStatus {
		case refund.StatusPendingManual:
			return MyOrderOutcomeRefundPendingManual
		case refund.StatusProcessing:
			return MyOrderOutcomeRefundProcessing
		case refund.StatusFailed:
			return MyOrderOutcomeRefundFailed
		case refund.StatusRejected:
			return MyOrderOutcomeRefundRejected
		case refund.StatusRefunded:
			return MyOrderOutcomeRefunded
		}
	}
	switch order.PaymentStatus {
	case payment.OrderStatusUnknown:
		return MyOrderOutcomePaymentConfirming
	case payment.OrderStatusPaidConfirmed:
		return MyOrderOutcomePaidConfirmed
	case payment.OrderStatusSettledZero:
		return MyOrderOutcomeSettledZero
	case payment.OrderStatusClosedUnpaid:
		return MyOrderOutcomeClosedUnpaid
	default:
		return MyOrderOutcomePendingPayment
	}
}

func validMyOrderReservation(value MyRegistrationItem) bool {
	if value.RegistrationID == uuid.Nil ||
		value.RegistrationVersion < 1 ||
		value.SeriesID == uuid.Nil ||
		value.InstanceID == uuid.Nil ||
		value.SessionID == uuid.Nil ||
		value.SessionStartAt.IsZero() ||
		value.SessionEndAt.IsZero() ||
		!value.SessionStartAt.Before(value.SessionEndAt) ||
		!ValidMyRegistrationState(value.State) ||
		value.State == MyRegistrationStateAll ||
		value.Order == nil ||
		!validMyOrderPaymentSummary(*value.Order) {
		return false
	}
	if value.Refund == nil {
		return true
	}
	return value.Order.PaymentStatus == payment.OrderStatusPaidConfirmed &&
		value.Refund.RefundCaseID != uuid.Nil &&
		validRefundStatus(value.Refund.RefundStatus) &&
		value.Refund.RequestedRefundCents > 0 &&
		value.Refund.SuccessfulRefundCents >= 0 &&
		value.Refund.SuccessfulRefundCents <=
			value.Refund.RequestedRefundCents &&
		value.Refund.Version >= 1 &&
		!value.Refund.UpdatedAt.IsZero()
}

func validMyOrderPaymentSummary(value MyRegistrationOrderSummary) bool {
	if value.OrderID == uuid.Nil ||
		!validPaymentStatus(value.PaymentStatus) ||
		value.OriginalPriceCents <= 0 ||
		value.DiscountCents < 0 ||
		value.DiscountCents > value.OriginalPriceCents ||
		value.PayableCents !=
			value.OriginalPriceCents-value.DiscountCents ||
		value.PayableCents < 0 ||
		!validHoldStatus(value.HoldStatus) ||
		value.HoldExpiresAt.IsZero() ||
		value.HoldVersion < 1 ||
		value.HoldUpdatedAt.IsZero() ||
		value.Version < 1 ||
		value.UpdatedAt.IsZero() {
		return false
	}
	switch value.PaymentStatus {
	case payment.OrderStatusPending, payment.OrderStatusUnknown:
		return value.PayableCents > 0 &&
			value.ActualPaidCents == nil &&
			value.PaidAt == nil &&
			value.ClosedAt == nil
	case payment.OrderStatusPaidConfirmed:
		return value.PayableCents > 0 &&
			value.ActualPaidCents != nil &&
			*value.ActualPaidCents == value.PayableCents &&
			value.PaidAt != nil
	case payment.OrderStatusSettledZero:
		return value.PayableCents == 0 &&
			value.DiscountCents == value.OriginalPriceCents &&
			value.ActualPaidCents == nil &&
			value.PaidAt == nil &&
			value.ClosedAt == nil
	case payment.OrderStatusClosedUnpaid:
		return value.PayableCents > 0 &&
			value.ActualPaidCents == nil &&
			value.PaidAt == nil &&
			value.ClosedAt != nil
	default:
		return false
	}
}

func cloneMyRegistrationRefund(
	value *MyRegistrationRefundSummary,
) *MyRegistrationRefundSummary {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.ResolvedAt = cloneTime(value.ResolvedAt)
	return &cloned
}
