// Package booking composes Xiangwan Activity, Registration, payment, Refund,
// and check-in axes into user-owned reservation read models.
package booking

import (
	"errors"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

const (
	DefaultMyRegistrationsLimit = 20
	MaxMyRegistrationsLimit     = 100
	MaxMyRegistrationsCursorAge = 30 * time.Minute
	MyRegistrationsFutureSkew   = 5 * time.Second
)

type MyRegistrationState string

const (
	MyRegistrationStateAll              MyRegistrationState = "all"
	MyRegistrationStatePendingPayment   MyRegistrationState = "pending_payment"
	MyRegistrationStateRegistered       MyRegistrationState = "registered"
	MyRegistrationStateCancelled        MyRegistrationState = "cancelled"
	MyRegistrationStateRefundProcessing MyRegistrationState = "refund_processing"
	MyRegistrationStateRefunded         MyRegistrationState = "refunded"
	MyRegistrationStateEnded            MyRegistrationState = "ended"
)

type CheckinStatus string

const (
	CheckinStatusNotRecorded CheckinStatus = "not_recorded"
	CheckinStatusCheckedIn   CheckinStatus = "checked_in"
	CheckinStatusRevoked     CheckinStatus = "revoked"
)

type CheckinSummary struct {
	Status      CheckinStatus
	CheckedInAt *time.Time
	RevokedAt   *time.Time
}

type MyRegistrationFacts struct {
	Registration registration.Registration
	SeriesTitle  string
	Instance     activity.Instance
	Session      activity.Session
	Order        *payment.Order
	Hold         *payment.CapacityHold
	Refund       *refund.Case
	Checkin      CheckinSummary
}

type MyRegistrationFilter struct {
	TenantID    uuid.UUID
	PrincipalID uuid.UUID
	State       MyRegistrationState
	Limit       int
	Cursor      string
	At          time.Time
}

type MyRegistrationOrderSummary struct {
	OrderID            uuid.UUID
	PaymentStatus      payment.OrderStatus
	OriginalPriceCents int64
	DiscountCents      int64
	PayableCents       int64
	ActualPaidCents    *int64
	PaidAt             *time.Time
	ClosedAt           *time.Time
	HoldStatus         payment.CapacityHoldStatus
	HoldExpiresAt      time.Time
	HoldVersion        int64
	HoldUpdatedAt      time.Time
	Version            int64
	UpdatedAt          time.Time
}

type MyRegistrationRefundSummary struct {
	RefundCaseID          uuid.UUID
	RefundStatus          refund.Status
	ReasonCode            refund.ReasonCode
	RequestedRefundCents  int64
	SuccessfulRefundCents int64
	ResolvedAt            *time.Time
	Version               int64
	UpdatedAt             time.Time
}

type MyRegistrationItem struct {
	RegistrationID      uuid.UUID
	RegistrationVersion int64
	SeriesID            uuid.UUID
	SeriesTitle         string
	InstanceID          uuid.UUID
	InstanceTitle       string
	InstanceStatus      activity.InstanceStatus
	SessionID           uuid.UUID
	SessionTitle        string
	SessionStatus       activity.SessionStatus
	SessionStartAt      time.Time
	SessionEndAt        time.Time
	DeliveryMode        activity.DeliveryMode
	Area                *activity.AreaCode
	VenueName           *string
	Address             *string
	OnlineMode          *string
	ParticipationStatus registration.ParticipationStatus
	ConfirmedAt         *time.Time
	CancelledAt         *time.Time
	CancellationReason  *string
	Order               *MyRegistrationOrderSummary
	Refund              *MyRegistrationRefundSummary
	Checkin             CheckinSummary
	State               MyRegistrationState
	HasActiveAccess     bool
	CanContinuePayment  bool
	LastBusinessAt      time.Time
	SortRank            int
	SortAt              time.Time
}

type MyRegistrationsPage struct {
	Items       []MyRegistrationItem
	ActiveState MyRegistrationState
	AsOf        time.Time
	NextCursor  string
}

var ErrInvalidMyRegistrationFacts = errors.New(
	"invalid xiangwan My Registration facts",
)

var (
	ErrInvalidMyRegistrationsFilter = errors.New(
		"invalid xiangwan My Registrations filter",
	)
	ErrInvalidMyRegistrationsCursor = errors.New(
		"invalid xiangwan My Registrations cursor",
	)
	ErrStaleMyRegistrationsCursor = errors.New(
		"stale xiangwan My Registrations cursor",
	)
)

func ProjectMyRegistration(
	facts MyRegistrationFacts,
	at time.Time,
) (MyRegistrationItem, error) {
	if err := validateMyRegistrationFacts(facts, at); err != nil {
		return MyRegistrationItem{}, err
	}
	asOf := at.UTC()
	state := classifyMyRegistration(facts, asOf)
	lastBusinessAt := myRegistrationLastBusinessAt(facts, state)
	sortRank := MyRegistrationStateSortRank(state)
	sortAt := lastBusinessAt
	if MyRegistrationStateSortsForward(state) {
		sortAt = facts.Session.SessionStartAt.UTC()
	}
	item := MyRegistrationItem{
		RegistrationID:      facts.Registration.ID,
		RegistrationVersion: facts.Registration.Version,
		SeriesID:            facts.Registration.SeriesID,
		SeriesTitle:         facts.SeriesTitle,
		InstanceID:          facts.Instance.ID,
		InstanceTitle:       facts.Instance.Title,
		InstanceStatus:      facts.Instance.Status,
		SessionID:           facts.Session.ID,
		SessionTitle:        facts.Session.Title,
		SessionStatus:       facts.Session.Status,
		SessionStartAt:      facts.Session.SessionStartAt.UTC(),
		SessionEndAt:        facts.Session.SessionEndAt.UTC(),
		DeliveryMode:        *facts.Session.DeliveryMode,
		Area:                cloneArea(facts.Session.Area),
		VenueName:           cloneString(facts.Session.VenueName),
		Address:             cloneString(facts.Session.Address),
		OnlineMode:          cloneString(facts.Session.OnlineParticipationMode),
		ParticipationStatus: facts.Registration.ParticipationStatus,
		ConfirmedAt:         cloneTime(facts.Registration.ConfirmedAt),
		CancelledAt:         cloneTime(facts.Registration.CancelledAt),
		CancellationReason:  cloneString(facts.Registration.CancellationReason),
		Checkin:             cloneCheckinSummary(facts.Checkin),
		State:               state,
		HasActiveAccess:     myRegistrationHasActiveAccess(facts, asOf),
		CanContinuePayment:  myRegistrationCanContinuePayment(facts, state, asOf),
		LastBusinessAt:      lastBusinessAt,
		SortRank:            sortRank,
		SortAt:              sortAt,
	}
	if facts.Order != nil && facts.Hold != nil {
		item.Order = myRegistrationOrderSummary(*facts.Order, *facts.Hold)
	}
	if facts.Refund != nil {
		item.Refund = myRegistrationRefundSummary(*facts.Refund)
	}
	return item, nil
}

func ValidMyRegistrationState(value MyRegistrationState) bool {
	switch value {
	case MyRegistrationStateAll,
		MyRegistrationStatePendingPayment,
		MyRegistrationStateRegistered,
		MyRegistrationStateCancelled,
		MyRegistrationStateRefundProcessing,
		MyRegistrationStateRefunded,
		MyRegistrationStateEnded:
		return true
	default:
		return false
	}
}

func MyRegistrationStateSortRank(value MyRegistrationState) int {
	switch value {
	case MyRegistrationStatePendingPayment:
		return 1
	case MyRegistrationStateRegistered:
		return 2
	case MyRegistrationStateRefundProcessing:
		return 3
	case MyRegistrationStateRefunded:
		return 4
	case MyRegistrationStateCancelled:
		return 5
	case MyRegistrationStateEnded:
		return 6
	default:
		return 0
	}
}

func MyRegistrationStateSortsForward(value MyRegistrationState) bool {
	return value == MyRegistrationStatePendingPayment ||
		value == MyRegistrationStateRegistered
}

func validateMyRegistrationFacts(
	facts MyRegistrationFacts,
	at time.Time,
) error {
	current := facts.Registration
	if at.IsZero() ||
		current.ID == uuid.Nil ||
		current.TenantID == uuid.Nil ||
		current.SeriesID == uuid.Nil ||
		current.InstanceID == uuid.Nil ||
		current.SessionID == uuid.Nil ||
		current.PrincipalID == uuid.Nil ||
		current.Version < 1 ||
		current.CreatedAt.IsZero() ||
		current.UpdatedAt.IsZero() ||
		strings.TrimSpace(facts.SeriesTitle) == "" ||
		facts.Instance.ID != current.InstanceID ||
		facts.Instance.TenantID != current.TenantID ||
		facts.Instance.SeriesID != current.SeriesID ||
		strings.TrimSpace(facts.Instance.Title) == "" ||
		facts.Instance.UpdatedAt.IsZero() ||
		facts.Session.ID != current.SessionID ||
		facts.Session.TenantID != current.TenantID ||
		facts.Session.InstanceID != current.InstanceID ||
		strings.TrimSpace(facts.Session.Title) == "" ||
		facts.Session.SessionStartAt == nil ||
		facts.Session.SessionEndAt == nil ||
		!facts.Session.SessionStartAt.Before(*facts.Session.SessionEndAt) ||
		facts.Session.DeliveryMode == nil ||
		facts.Session.UpdatedAt.IsZero() ||
		!validParticipationStatus(current.ParticipationStatus) ||
		!validInstanceStatus(facts.Instance.Status) ||
		!validSessionStatus(facts.Session.Status) ||
		!validCheckinSummary(facts.Checkin) {
		return ErrInvalidMyRegistrationFacts
	}
	if err := validateMyRegistrationOrder(facts); err != nil {
		return err
	}
	if err := validateMyRegistrationRefund(facts); err != nil {
		return err
	}
	return nil
}

func validateMyRegistrationOrder(facts MyRegistrationFacts) error {
	if facts.Order == nil {
		if facts.Hold != nil {
			return ErrInvalidMyRegistrationFacts
		}
		return nil
	}
	if facts.Hold == nil {
		return ErrInvalidMyRegistrationFacts
	}
	current := facts.Registration
	order := facts.Order
	hold := facts.Hold
	if order.ID == uuid.Nil ||
		order.TenantID != current.TenantID ||
		order.RegistrationID != current.ID ||
		order.SeriesID != current.SeriesID ||
		order.InstanceID != current.InstanceID ||
		order.SessionID != current.SessionID ||
		order.PrincipalID != current.PrincipalID ||
		order.Version < 1 ||
		order.OriginalPriceCents < 0 ||
		order.DiscountCents < 0 ||
		order.PayableCents < 0 ||
		order.OriginalPriceCents-order.DiscountCents != order.PayableCents ||
		order.UpdatedAt.IsZero() ||
		!validPaymentStatus(order.PaymentStatus) ||
		hold.ID == uuid.Nil ||
		hold.TenantID != current.TenantID ||
		hold.OrderID != order.ID ||
		hold.RegistrationID != current.ID ||
		hold.SessionID != current.SessionID ||
		hold.ExpiresAt.IsZero() ||
		hold.Version < 1 ||
		hold.UpdatedAt.IsZero() ||
		!validHoldStatus(hold.HoldStatus) {
		return ErrInvalidMyRegistrationFacts
	}
	return nil
}

func validateMyRegistrationRefund(facts MyRegistrationFacts) error {
	if facts.Refund == nil {
		return nil
	}
	if facts.Order == nil {
		return ErrInvalidMyRegistrationFacts
	}
	current := facts.Registration
	value := facts.Refund
	if value.ID == uuid.Nil ||
		value.TenantID != current.TenantID ||
		value.OrderID != facts.Order.ID ||
		value.RegistrationID != current.ID ||
		value.SeriesID != current.SeriesID ||
		value.InstanceID != current.InstanceID ||
		value.SessionID != current.SessionID ||
		value.PrincipalID != current.PrincipalID ||
		value.Version < 1 ||
		value.RequestedRefundCents <= 0 ||
		value.SuccessfulRefundCents < 0 ||
		value.SuccessfulRefundCents > value.RequestedRefundCents ||
		value.UpdatedAt.IsZero() ||
		!validRefundStatus(value.RefundStatus) {
		return ErrInvalidMyRegistrationFacts
	}
	return nil
}

func classifyMyRegistration(
	facts MyRegistrationFacts,
	asOf time.Time,
) MyRegistrationState {
	if facts.Refund != nil {
		switch facts.Refund.RefundStatus {
		case refund.StatusRefunded:
			return MyRegistrationStateRefunded
		case refund.StatusPendingManual,
			refund.StatusProcessing,
			refund.StatusFailed:
			return MyRegistrationStateRefundProcessing
		}
	}
	if facts.Registration.ParticipationStatus ==
		registration.ParticipationStatusCancelled ||
		facts.Instance.Status == activity.InstanceStatusCancelled ||
		facts.Session.Status == activity.SessionStatusCancelled {
		return MyRegistrationStateCancelled
	}
	if facts.Instance.Status == activity.InstanceStatusCompleted ||
		facts.Instance.Status == activity.InstanceStatusArchived ||
		facts.Session.Status == activity.SessionStatusEnded ||
		facts.Session.Status == activity.SessionStatusArchived ||
		!asOf.Before(*facts.Session.SessionEndAt) {
		return MyRegistrationStateEnded
	}
	if facts.Registration.ParticipationStatus ==
		registration.ParticipationStatusPendingPayment {
		return MyRegistrationStatePendingPayment
	}
	return MyRegistrationStateRegistered
}

func myRegistrationHasActiveAccess(
	facts MyRegistrationFacts,
	asOf time.Time,
) bool {
	return facts.Registration.ParticipationStatus ==
		registration.ParticipationStatusConfirmed &&
		facts.Instance.Status == activity.InstanceStatusPublished &&
		facts.Session.Status == activity.SessionStatusPublished &&
		asOf.Before(*facts.Session.SessionEndAt)
}

func myRegistrationCanContinuePayment(
	facts MyRegistrationFacts,
	state MyRegistrationState,
	asOf time.Time,
) bool {
	return state == MyRegistrationStatePendingPayment &&
		facts.Order != nil &&
		facts.Hold != nil &&
		facts.Order.PaymentStatus == payment.OrderStatusPending &&
		facts.Hold.HoldStatus == payment.CapacityHoldStatusActive &&
		asOf.Before(facts.Hold.ExpiresAt) &&
		facts.Instance.Status == activity.InstanceStatusPublished &&
		facts.Session.Status == activity.SessionStatusPublished
}

func myRegistrationLastBusinessAt(
	facts MyRegistrationFacts,
	state MyRegistrationState,
) time.Time {
	value := latestTime(
		facts.Registration.UpdatedAt,
		facts.Instance.UpdatedAt,
		facts.Session.UpdatedAt,
	)
	if facts.Order != nil {
		value = latestTime(value, facts.Order.UpdatedAt)
	}
	if facts.Hold != nil {
		value = latestTime(value, facts.Hold.UpdatedAt)
	}
	if facts.Refund != nil {
		value = latestTime(value, facts.Refund.UpdatedAt)
	}
	if state == MyRegistrationStateEnded {
		value = latestTime(value, *facts.Session.SessionEndAt)
	}
	if facts.Checkin.CheckedInAt != nil {
		value = latestTime(value, *facts.Checkin.CheckedInAt)
	}
	if facts.Checkin.RevokedAt != nil {
		value = latestTime(value, *facts.Checkin.RevokedAt)
	}
	return value.UTC()
}

func myRegistrationOrderSummary(
	order payment.Order,
	hold payment.CapacityHold,
) *MyRegistrationOrderSummary {
	return &MyRegistrationOrderSummary{
		OrderID:            order.ID,
		PaymentStatus:      order.PaymentStatus,
		OriginalPriceCents: order.OriginalPriceCents,
		DiscountCents:      order.DiscountCents,
		PayableCents:       order.PayableCents,
		ActualPaidCents:    cloneInt64(order.ActualPaidCents),
		PaidAt:             cloneTime(order.PaidAt),
		ClosedAt:           cloneTime(order.ClosedAt),
		HoldStatus:         hold.HoldStatus,
		HoldExpiresAt:      hold.ExpiresAt.UTC(),
		HoldVersion:        hold.Version,
		HoldUpdatedAt:      hold.UpdatedAt.UTC(),
		Version:            order.Version,
		UpdatedAt:          order.UpdatedAt.UTC(),
	}
}

func myRegistrationRefundSummary(
	value refund.Case,
) *MyRegistrationRefundSummary {
	return &MyRegistrationRefundSummary{
		RefundCaseID:          value.ID,
		RefundStatus:          value.RefundStatus,
		ReasonCode:            value.ReasonCode,
		RequestedRefundCents:  value.RequestedRefundCents,
		SuccessfulRefundCents: value.SuccessfulRefundCents,
		ResolvedAt:            cloneTime(value.ResolvedAt),
		Version:               value.Version,
		UpdatedAt:             value.UpdatedAt.UTC(),
	}
}

func validParticipationStatus(value registration.ParticipationStatus) bool {
	switch value {
	case registration.ParticipationStatusPendingPayment,
		registration.ParticipationStatusConfirmed,
		registration.ParticipationStatusCancelled:
		return true
	default:
		return false
	}
}

func validInstanceStatus(value activity.InstanceStatus) bool {
	switch value {
	case activity.InstanceStatusPublished,
		activity.InstanceStatusCompleted,
		activity.InstanceStatusCancelled,
		activity.InstanceStatusArchived:
		return true
	default:
		return false
	}
}

func validSessionStatus(value activity.SessionStatus) bool {
	switch value {
	case activity.SessionStatusPublished,
		activity.SessionStatusCancelled,
		activity.SessionStatusEnded,
		activity.SessionStatusArchived:
		return true
	default:
		return false
	}
}

func validPaymentStatus(value payment.OrderStatus) bool {
	switch value {
	case payment.OrderStatusPending,
		payment.OrderStatusUnknown,
		payment.OrderStatusPaidConfirmed,
		payment.OrderStatusSettledZero,
		payment.OrderStatusClosedUnpaid:
		return true
	default:
		return false
	}
}

func validHoldStatus(value payment.CapacityHoldStatus) bool {
	switch value {
	case payment.CapacityHoldStatusActive,
		payment.CapacityHoldStatusConverted,
		payment.CapacityHoldStatusReleased,
		payment.CapacityHoldStatusExpired:
		return true
	default:
		return false
	}
}

func validRefundStatus(value refund.Status) bool {
	switch value {
	case refund.StatusPendingManual,
		refund.StatusProcessing,
		refund.StatusRefunded,
		refund.StatusFailed,
		refund.StatusRejected:
		return true
	default:
		return false
	}
}

func validCheckinSummary(value CheckinSummary) bool {
	switch value.Status {
	case "", CheckinStatusNotRecorded:
		return value.CheckedInAt == nil && value.RevokedAt == nil
	case CheckinStatusCheckedIn:
		return value.CheckedInAt != nil && value.RevokedAt == nil
	case CheckinStatusRevoked:
		return value.CheckedInAt != nil &&
			value.RevokedAt != nil &&
			!value.RevokedAt.Before(*value.CheckedInAt)
	default:
		return false
	}
}

func cloneCheckinSummary(value CheckinSummary) CheckinSummary {
	if value.Status == "" {
		value.Status = CheckinStatusNotRecorded
	}
	value.CheckedInAt = cloneTime(value.CheckedInAt)
	value.RevokedAt = cloneTime(value.RevokedAt)
	return value
}

func latestTime(values ...time.Time) time.Time {
	var latest time.Time
	for _, value := range values {
		if value.After(latest) {
			latest = value
		}
	}
	return latest
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneArea(value *activity.AreaCode) *activity.AreaCode {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
