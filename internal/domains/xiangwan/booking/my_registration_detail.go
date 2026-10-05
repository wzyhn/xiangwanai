package booking

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

type MyRegistrationCancellationScope string

const (
	MyRegistrationCancellationScopeRegistration MyRegistrationCancellationScope = "registration"
	MyRegistrationCancellationScopeSession      MyRegistrationCancellationScope = "session"
	MyRegistrationCancellationScopeInstance     MyRegistrationCancellationScope = "instance"
)

type MyRegistrationCancellationFact struct {
	ReceiptID  uuid.UUID
	Scope      MyRegistrationCancellationScope
	SeriesID   uuid.UUID
	InstanceID uuid.UUID
	SessionID  *uuid.UUID
	Reason     string
	At         time.Time
}

type MyRegistrationContactFacts struct {
	Name      string
	PhoneE164 string
}

type MyRegistrationContact struct {
	Name        string
	PhoneMasked string
}

type MyRegistrationDetailFacts struct {
	Item                 MyRegistrationItem
	Contact              MyRegistrationContactFacts
	SessionCancellation  *MyRegistrationCancellationFact
	InstanceCancellation *MyRegistrationCancellationFact
	CouponAdjustment     *MyRegistrationCouponAdjustment
}

type MyRegistrationCancellationSummary struct {
	ReceiptID *uuid.UUID
	Scope     MyRegistrationCancellationScope
	Reason    string
	At        time.Time
}

type MyRegistrationCouponAdjustment struct {
	Disposition   coupon.RefundDisposition
	PolicyVersion string
	OccurredAt    time.Time
}

type MyRegistrationAccessDenial string

const (
	MyRegistrationAccessAllowed               MyRegistrationAccessDenial = ""
	MyRegistrationAccessRegistrationCancelled MyRegistrationAccessDenial = "registration_cancelled"
	MyRegistrationAccessSessionCancelled      MyRegistrationAccessDenial = "session_cancelled"
	MyRegistrationAccessInstanceCancelled     MyRegistrationAccessDenial = "instance_cancelled"
	MyRegistrationAccessSessionEnded          MyRegistrationAccessDenial = "session_ended"
	MyRegistrationAccessUnavailable           MyRegistrationAccessDenial = "unavailable"
)

type MyRegistrationCancellationAction string

const (
	MyRegistrationCancellationActionAvailable   MyRegistrationCancellationAction = "available"
	MyRegistrationCancellationActionUnavailable MyRegistrationCancellationAction = "unavailable"
)

type MyRegistrationDetail struct {
	Item                       MyRegistrationItem
	Contact                    MyRegistrationContact
	Cancellation               *MyRegistrationCancellationSummary
	CouponAdjustment           *MyRegistrationCouponAdjustment
	AccessDenial               MyRegistrationAccessDenial
	CheckinCredentialEligible  bool
	PrivateAccessEligible      bool
	CancellationAction         MyRegistrationCancellationAction
	CancellationPolicyRequired bool
	AsOf                       time.Time
}

var ErrInvalidMyRegistrationDetailFacts = errors.New(
	"invalid xiangwan My Registration detail facts",
)

var myRegistrationContactPhonePattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

var (
	ErrInvalidMyRegistrationIdentity = errors.New(
		"invalid xiangwan My Registration identity",
	)
	ErrMyRegistrationNotFound = errors.New(
		"xiangwan My Registration not found",
	)
	ErrMyRegistrationCancellationPolicy = errors.New(
		"xiangwan My Registration cancellation policy failed",
	)
)

func ProjectMyRegistrationDetail(
	facts MyRegistrationDetailFacts,
	asOf time.Time,
) (MyRegistrationDetail, error) {
	if asOf.IsZero() || !validMyRegistrationDetailItem(facts.Item) ||
		!validMyRegistrationContact(facts.Contact) {
		return MyRegistrationDetail{}, ErrInvalidMyRegistrationDetailFacts
	}
	if !validCancellationFact(
		facts.SessionCancellation,
		facts.Item,
		MyRegistrationCancellationScopeSession,
	) || !validCancellationFact(
		facts.InstanceCancellation,
		facts.Item,
		MyRegistrationCancellationScopeInstance,
	) {
		return MyRegistrationDetail{}, ErrInvalidMyRegistrationDetailFacts
	}
	if !validMyRegistrationCouponAdjustment(
		facts.CouponAdjustment,
		facts.Item,
	) {
		return MyRegistrationDetail{}, ErrInvalidMyRegistrationDetailFacts
	}

	cancellation := selectRegistrationCancellation(facts)
	accessDenial := myRegistrationAccessDenial(facts.Item)
	accessAllowed := accessDenial == MyRegistrationAccessAllowed
	policyRequired := myRegistrationCancellationPolicyRequired(facts.Item)
	return MyRegistrationDetail{
		Item:                       cloneMyRegistrationItem(facts.Item),
		Contact:                    projectMyRegistrationContact(facts.Contact),
		Cancellation:               cancellation,
		CouponAdjustment:           cloneMyRegistrationCouponAdjustment(facts.CouponAdjustment),
		AccessDenial:               accessDenial,
		CheckinCredentialEligible:  accessAllowed,
		PrivateAccessEligible:      accessAllowed,
		CancellationAction:         myRegistrationCancellationAction(facts.Item, asOf),
		CancellationPolicyRequired: policyRequired,
		AsOf:                       asOf.UTC(),
	}, nil
}

func validMyRegistrationContact(value MyRegistrationContactFacts) bool {
	if value.Name == "" && value.PhoneE164 == "" {
		return true
	}
	name := strings.TrimSpace(value.Name)
	return value.Name == name && utf8.ValidString(name) &&
		utf8.RuneCountInString(name) >= 1 && utf8.RuneCountInString(name) <= 100 &&
		myRegistrationContactPhonePattern.MatchString(value.PhoneE164)
}

func projectMyRegistrationContact(value MyRegistrationContactFacts) MyRegistrationContact {
	if value.Name == "" && value.PhoneE164 == "" {
		return MyRegistrationContact{}
	}
	return MyRegistrationContact{
		Name:        value.Name,
		PhoneMasked: maskMyRegistrationPhoneE164(value.PhoneE164),
	}
}

func maskMyRegistrationPhoneE164(value string) string {
	const shortInternationalPhoneMaxDigits = 10

	digits := value[1:]
	if len(digits) <= shortInternationalPhoneMaxDigits {
		return "+" + digits[:1] + "****" + digits[len(digits)-2:]
	}
	return "+" + digits[:3] + "****" + digits[len(digits)-4:]
}

func validMyRegistrationDetailItem(value MyRegistrationItem) bool {
	return value.RegistrationID != uuid.Nil &&
		value.RegistrationVersion >= 1 &&
		value.SeriesID != uuid.Nil &&
		strings.TrimSpace(value.SeriesTitle) != "" &&
		value.InstanceID != uuid.Nil &&
		strings.TrimSpace(value.InstanceTitle) != "" &&
		value.SessionID != uuid.Nil &&
		strings.TrimSpace(value.SessionTitle) != "" &&
		!value.SessionStartAt.IsZero() &&
		!value.SessionEndAt.IsZero() &&
		value.SessionStartAt.Before(value.SessionEndAt) &&
		ValidMyRegistrationState(value.State) &&
		value.State != MyRegistrationStateAll
}

func validCancellationFact(
	value *MyRegistrationCancellationFact,
	item MyRegistrationItem,
	scope MyRegistrationCancellationScope,
) bool {
	if value == nil {
		return true
	}
	if value.ReceiptID == uuid.Nil ||
		value.Scope != scope ||
		value.SeriesID != item.SeriesID ||
		value.InstanceID != item.InstanceID ||
		strings.TrimSpace(value.Reason) == "" ||
		value.At.IsZero() {
		return false
	}
	if scope == MyRegistrationCancellationScopeSession {
		return value.SessionID != nil &&
			*value.SessionID == item.SessionID &&
			item.SessionStatus == activity.SessionStatusCancelled
	}
	return value.SessionID == nil &&
		item.InstanceStatus == activity.InstanceStatusCancelled
}

func selectRegistrationCancellation(
	facts MyRegistrationDetailFacts,
) *MyRegistrationCancellationSummary {
	if facts.InstanceCancellation != nil {
		return cancellationSummary(*facts.InstanceCancellation)
	}
	if facts.SessionCancellation != nil {
		return cancellationSummary(*facts.SessionCancellation)
	}
	if facts.Item.CancelledAt == nil ||
		facts.Item.CancellationReason == nil {
		return nil
	}
	return &MyRegistrationCancellationSummary{
		Scope:  MyRegistrationCancellationScopeRegistration,
		Reason: *facts.Item.CancellationReason,
		At:     facts.Item.CancelledAt.UTC(),
	}
}

func cancellationSummary(
	value MyRegistrationCancellationFact,
) *MyRegistrationCancellationSummary {
	receiptID := value.ReceiptID
	return &MyRegistrationCancellationSummary{
		ReceiptID: &receiptID,
		Scope:     value.Scope,
		Reason:    value.Reason,
		At:        value.At.UTC(),
	}
}

func myRegistrationAccessDenial(
	item MyRegistrationItem,
) MyRegistrationAccessDenial {
	switch {
	case item.InstanceStatus == activity.InstanceStatusCancelled:
		return MyRegistrationAccessInstanceCancelled
	case item.SessionStatus == activity.SessionStatusCancelled:
		return MyRegistrationAccessSessionCancelled
	case item.ParticipationStatus ==
		registration.ParticipationStatusCancelled:
		return MyRegistrationAccessRegistrationCancelled
	case item.State == MyRegistrationStateEnded:
		return MyRegistrationAccessSessionEnded
	case !item.HasActiveAccess:
		return MyRegistrationAccessUnavailable
	default:
		return MyRegistrationAccessAllowed
	}
}

func myRegistrationCancellationAction(
	item MyRegistrationItem,
	asOf time.Time,
) MyRegistrationCancellationAction {
	if item.State != MyRegistrationStatePendingPayment &&
		item.State != MyRegistrationStateRegistered {
		return MyRegistrationCancellationActionUnavailable
	}
	if myRegistrationCancellationPolicyRequired(item) {
		return MyRegistrationCancellationActionUnavailable
	}
	allowed, err := registration.SelfCancellationWithinCutoff(
		item.SessionStartAt,
		asOf,
		0,
	)
	if err != nil || !allowed {
		return MyRegistrationCancellationActionUnavailable
	}
	return MyRegistrationCancellationActionAvailable
}

// ApplyMyRegistrationSelfCancellationPolicy replaces the default Session-start
// cutoff with the configured registration-specific decision. The write command
// invokes the same policy again after locking the Session and Registration.
func ApplyMyRegistrationSelfCancellationPolicy(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	detail MyRegistrationDetail,
	policy registration.SelfCancellationPolicy,
) (MyRegistrationDetail, error) {
	if detail.CancellationAction != MyRegistrationCancellationActionAvailable {
		return detail, nil
	}
	input := registration.SelfCancellationPolicyInput{
		TenantID:       tenantID,
		RegistrationID: detail.Item.RegistrationID,
		PrincipalID:    principalID,
		SessionID:      detail.Item.SessionID,
		SessionStartAt: detail.Item.SessionStartAt,
		EvaluatedAt:    detail.AsOf,
	}
	var (
		decision registration.SelfCancellationPolicyDecision
		err      error
	)
	if policy == nil {
		decision, err = registration.EvaluateSelfCancellationCutoff(
			input,
			"",
			0,
		)
	} else {
		decision, err = policy.EvaluateSelfCancellation(ctx, input)
	}
	if err != nil {
		return MyRegistrationDetail{}, fmt.Errorf(
			"%w: %v",
			ErrMyRegistrationCancellationPolicy,
			err,
		)
	}
	if err := registration.ValidateSelfCancellationPolicyDecision(decision); err != nil {
		return MyRegistrationDetail{}, fmt.Errorf(
			"%w: %v",
			ErrMyRegistrationCancellationPolicy,
			err,
		)
	}
	if !decision.Allowed {
		detail.CancellationAction = MyRegistrationCancellationActionUnavailable
	}
	return detail, nil
}

func myRegistrationCancellationPolicyRequired(item MyRegistrationItem) bool {
	if item.State != MyRegistrationStatePendingPayment &&
		item.State != MyRegistrationStateRegistered ||
		item.Order == nil {
		return false
	}
	switch item.Order.PaymentStatus {
	case payment.OrderStatusPaidConfirmed,
		payment.OrderStatusUnknown,
		payment.OrderStatusSettledZero:
		return true
	default:
		return false
	}
}

func validMyRegistrationCouponAdjustment(
	value *MyRegistrationCouponAdjustment,
	item MyRegistrationItem,
) bool {
	required := item.ParticipationStatus == registration.ParticipationStatusCancelled &&
		item.CancellationReason != nil &&
		strings.HasPrefix(*item.CancellationReason, "user_cancelled@") &&
		item.Order != nil &&
		item.Order.PaymentStatus == payment.OrderStatusSettledZero
	if value == nil {
		return !required
	}
	if item.ParticipationStatus != registration.ParticipationStatusCancelled ||
		item.CancelledAt == nil || item.Order == nil || value.OccurredAt.IsZero() ||
		value.OccurredAt.Before(*item.CancelledAt) ||
		coupon.ValidateRefundPolicyDecision(coupon.RefundPolicyDecision{
			Configured:    true,
			PolicyVersion: value.PolicyVersion,
			Disposition:   value.Disposition,
		}) != nil {
		return false
	}
	switch item.Order.PaymentStatus {
	case payment.OrderStatusSettledZero:
		return true
	case payment.OrderStatusPaidConfirmed:
		return item.Refund != nil && item.Refund.RefundStatus == refund.StatusRefunded
	default:
		return false
	}
}

func cloneMyRegistrationCouponAdjustment(
	value *MyRegistrationCouponAdjustment,
) *MyRegistrationCouponAdjustment {
	if value == nil {
		return nil
	}
	result := *value
	result.OccurredAt = result.OccurredAt.UTC()
	return &result
}

func cloneMyRegistrationItem(value MyRegistrationItem) MyRegistrationItem {
	value.Area = cloneArea(value.Area)
	value.VenueName = cloneString(value.VenueName)
	value.Address = cloneString(value.Address)
	value.OnlineMode = cloneString(value.OnlineMode)
	value.ConfirmedAt = cloneTime(value.ConfirmedAt)
	value.CancelledAt = cloneTime(value.CancelledAt)
	value.CancellationReason = cloneString(value.CancellationReason)
	value.Checkin = cloneCheckinSummary(value.Checkin)
	if value.Order != nil {
		order := *value.Order
		order.ActualPaidCents = cloneInt64(value.Order.ActualPaidCents)
		order.PaidAt = cloneTime(value.Order.PaidAt)
		order.ClosedAt = cloneTime(value.Order.ClosedAt)
		value.Order = &order
	}
	value.Refund = cloneMyRegistrationRefund(value.Refund)
	return value
}
