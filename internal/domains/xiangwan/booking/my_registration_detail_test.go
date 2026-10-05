package booking

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

func TestProjectMyRegistrationDetailAppliesAccessAndCancellationGates(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name             string
		mutateFacts      func(*MyRegistrationFacts)
		wantDenial       MyRegistrationAccessDenial
		wantCredential   bool
		wantCancelAction MyRegistrationCancellationAction
		wantPolicy       bool
		wantScope        MyRegistrationCancellationScope
	}{
		{
			name:             "active free registration",
			wantCredential:   true,
			wantCancelAction: MyRegistrationCancellationActionAvailable,
		},
		{
			name: "running free registration cannot cancel after the default cutoff",
			mutateFacts: func(facts *MyRegistrationFacts) {
				start := now.Add(-time.Hour)
				end := now.Add(time.Hour)
				facts.Session.SessionStartAt = &start
				facts.Session.SessionEndAt = &end
			},
			wantCredential:   true,
			wantCancelAction: MyRegistrationCancellationActionUnavailable,
		},
		{
			name: "pending payment can cancel but has no access",
			mutateFacts: func(facts *MyRegistrationFacts) {
				makePendingPaymentFacts(facts, now)
			},
			wantDenial:       MyRegistrationAccessUnavailable,
			wantCancelAction: MyRegistrationCancellationActionAvailable,
		},
		{
			name: "paid registration stays closed without an accessible final policy decision",
			mutateFacts: func(facts *MyRegistrationFacts) {
				makePaidMyOrderFacts(facts, now)
			},
			wantCredential:   true,
			wantCancelAction: MyRegistrationCancellationActionUnavailable,
			wantPolicy:       true,
		},
		{
			name: "self cancellation revokes every access",
			mutateFacts: func(facts *MyRegistrationFacts) {
				cancelMyRegistrationFacts(facts, now.Add(time.Minute))
			},
			wantDenial:       MyRegistrationAccessRegistrationCancelled,
			wantCancelAction: MyRegistrationCancellationActionUnavailable,
			wantScope:        MyRegistrationCancellationScopeRegistration,
		},
		{
			name: "ended registration has no access",
			mutateFacts: func(facts *MyRegistrationFacts) {
				start := now.Add(-2 * time.Hour)
				end := now.Add(-time.Hour)
				facts.Session.SessionStartAt = &start
				facts.Session.SessionEndAt = &end
				facts.Session.Status = activity.SessionStatusEnded
			},
			wantDenial:       MyRegistrationAccessSessionEnded,
			wantCancelAction: MyRegistrationCancellationActionUnavailable,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			facts := myRegistrationFacts(now)
			if test.mutateFacts != nil {
				test.mutateFacts(&facts)
			}
			item, err := ProjectMyRegistration(facts, now)
			if err != nil {
				t.Fatalf("ProjectMyRegistration() error = %v", err)
			}
			detail, err := ProjectMyRegistrationDetail(
				MyRegistrationDetailFacts{
					Item:    item,
					Contact: validMyRegistrationContactFacts(),
				},
				now,
			)
			if err != nil {
				t.Fatalf("ProjectMyRegistrationDetail() error = %v", err)
			}
			if detail.AccessDenial != test.wantDenial ||
				detail.CheckinCredentialEligible != test.wantCredential ||
				detail.PrivateAccessEligible != test.wantCredential ||
				detail.CancellationAction != test.wantCancelAction ||
				detail.CancellationPolicyRequired != test.wantPolicy ||
				!detail.AsOf.Equal(now) {
				t.Fatalf("detail = %+v", detail)
			}
			if detail.Contact.Name != "王薇" ||
				detail.Contact.PhoneMasked != "+861****5678" {
				t.Fatalf("contact = %+v", detail.Contact)
			}
			if test.wantScope == "" {
				if detail.Cancellation != nil {
					t.Fatalf("unexpected cancellation = %+v", detail.Cancellation)
				}
			} else if detail.Cancellation == nil ||
				detail.Cancellation.Scope != test.wantScope {
				t.Fatalf("cancellation = %+v", detail.Cancellation)
			}
		})
	}
}

func TestProjectMyRegistrationDetailRequiresAndRestoresCouponAdjustment(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 9, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(now)
	makePendingPaymentFacts(&facts, now)
	confirmedAt := now.Add(-time.Minute)
	facts.Registration.ParticipationStatus = registration.ParticipationStatusConfirmed
	facts.Registration.ConfirmedAt = &confirmedAt
	facts.Order.PaymentStatus = payment.OrderStatusSettledZero
	facts.Order.DiscountCents = facts.Order.OriginalPriceCents
	facts.Order.PayableCents = 0
	facts.Hold.HoldStatus = payment.CapacityHoldStatusConverted
	cancelledAt := now.Add(time.Minute)
	cancelMyRegistrationFacts(&facts, cancelledAt)
	reason := "user_cancelled@cancel-v3"
	facts.Registration.CancellationReason = &reason
	item, err := ProjectMyRegistration(facts, now)
	if err != nil {
		t.Fatalf("ProjectMyRegistration() error = %v", err)
	}

	if _, err = ProjectMyRegistrationDetail(
		MyRegistrationDetailFacts{
			Item:    item,
			Contact: validMyRegistrationContactFacts(),
		},
		now,
	); !errors.Is(err, ErrInvalidMyRegistrationDetailFacts) {
		t.Fatalf("ProjectMyRegistrationDetail(missing adjustment) error = %v", err)
	}

	detail, err := ProjectMyRegistrationDetail(MyRegistrationDetailFacts{
		Item:    item,
		Contact: validMyRegistrationContactFacts(),
		CouponAdjustment: &MyRegistrationCouponAdjustment{
			Disposition:   coupon.RefundDispositionRestore,
			PolicyVersion: "coupon-refund-v2",
			OccurredAt:    cancelledAt,
		},
	}, now)
	if err != nil {
		t.Fatalf("ProjectMyRegistrationDetail() error = %v", err)
	}
	if detail.CouponAdjustment == nil ||
		detail.CouponAdjustment.Disposition != coupon.RefundDispositionRestore ||
		detail.CouponAdjustment.PolicyVersion != "coupon-refund-v2" ||
		!detail.CouponAdjustment.OccurredAt.Equal(cancelledAt) {
		t.Fatalf("coupon adjustment = %+v", detail.CouponAdjustment)
	}
}

func TestProjectMyRegistrationDetailPrefersInstanceCancellation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 9, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(now)
	cancelMyRegistrationFacts(&facts, now.Add(time.Minute))
	facts.Instance.Status = activity.InstanceStatusCancelled
	facts.Instance.UpdatedAt = now.Add(2 * time.Minute)
	facts.Session.Status = activity.SessionStatusCancelled
	facts.Session.UpdatedAt = now.Add(2 * time.Minute)
	item, err := ProjectMyRegistration(facts, now)
	if err != nil {
		t.Fatalf("ProjectMyRegistration() error = %v", err)
	}
	sessionID := item.SessionID
	sessionReceiptID := uuid.New()
	instanceReceiptID := uuid.New()
	detail, err := ProjectMyRegistrationDetail(MyRegistrationDetailFacts{
		Item:    item,
		Contact: validMyRegistrationContactFacts(),
		SessionCancellation: &MyRegistrationCancellationFact{
			ReceiptID:  sessionReceiptID,
			Scope:      MyRegistrationCancellationScopeSession,
			SeriesID:   item.SeriesID,
			InstanceID: item.InstanceID,
			SessionID:  &sessionID,
			Reason:     "Venue unavailable",
			At:         now.Add(2 * time.Minute),
		},
		InstanceCancellation: &MyRegistrationCancellationFact{
			ReceiptID:  instanceReceiptID,
			Scope:      MyRegistrationCancellationScopeInstance,
			SeriesID:   item.SeriesID,
			InstanceID: item.InstanceID,
			Reason:     "Instance cancelled",
			At:         now.Add(2 * time.Minute),
		},
	}, now)
	if err != nil {
		t.Fatalf("ProjectMyRegistrationDetail() error = %v", err)
	}
	if detail.Cancellation == nil ||
		detail.Cancellation.Scope !=
			MyRegistrationCancellationScopeInstance ||
		detail.Cancellation.ReceiptID == nil ||
		*detail.Cancellation.ReceiptID != instanceReceiptID ||
		detail.AccessDenial != MyRegistrationAccessInstanceCancelled {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestProjectMyRegistrationDetailRejectsCrossHierarchyReceipt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 9, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(now)
	item, err := ProjectMyRegistration(facts, now)
	if err != nil {
		t.Fatalf("ProjectMyRegistration() error = %v", err)
	}
	sessionID := uuid.New()
	_, err = ProjectMyRegistrationDetail(MyRegistrationDetailFacts{
		Item:    item,
		Contact: validMyRegistrationContactFacts(),
		SessionCancellation: &MyRegistrationCancellationFact{
			ReceiptID:  uuid.New(),
			Scope:      MyRegistrationCancellationScopeSession,
			SeriesID:   item.SeriesID,
			InstanceID: item.InstanceID,
			SessionID:  &sessionID,
			Reason:     "Wrong Session",
			At:         now,
		},
	}, now)
	if !errors.Is(err, ErrInvalidMyRegistrationDetailFacts) {
		t.Fatalf("ProjectMyRegistrationDetail() error = %v", err)
	}
}

func TestProjectMyRegistrationDetailClonesMutableInput(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 9, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(now)
	makePaidMyOrderFacts(&facts, now)
	item, err := ProjectMyRegistration(facts, now)
	if err != nil {
		t.Fatalf("ProjectMyRegistration() error = %v", err)
	}
	detail, err := ProjectMyRegistrationDetail(
		MyRegistrationDetailFacts{
			Item:    item,
			Contact: validMyRegistrationContactFacts(),
		},
		now,
	)
	if err != nil {
		t.Fatalf("ProjectMyRegistrationDetail() error = %v", err)
	}
	*item.Order.ActualPaidCents = 1
	if detail.Item.Order.ActualPaidCents == nil ||
		*detail.Item.Order.ActualPaidCents != facts.Order.PayableCents {
		t.Fatal("detail aliased a nullable Order amount")
	}
}

func TestProjectMyRegistrationDetailRejectsMalformedContact(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 9, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(now)
	item, err := ProjectMyRegistration(facts, now)
	if err != nil {
		t.Fatalf("ProjectMyRegistration() error = %v", err)
	}
	for _, contact := range []MyRegistrationContactFacts{
		{Name: "王薇"},
		{PhoneE164: "+8613812345678"},
		{Name: " 王薇", PhoneE164: "+8613812345678"},
		{Name: strings.Repeat("名", 101), PhoneE164: "+8613812345678"},
		{Name: "王薇", PhoneE164: "13812345678"},
	} {
		if _, err := ProjectMyRegistrationDetail(MyRegistrationDetailFacts{
			Item:    item,
			Contact: contact,
		}, now); !errors.Is(err, ErrInvalidMyRegistrationDetailFacts) {
			t.Fatalf("ProjectMyRegistrationDetail(contact=%+v) error = %v", contact, err)
		}
	}
}

func TestProjectMyRegistrationDetailAcceptsLegacyMissingContact(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 9, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(now)
	item, err := ProjectMyRegistration(facts, now)
	if err != nil {
		t.Fatalf("ProjectMyRegistration() error = %v", err)
	}
	detail, err := ProjectMyRegistrationDetail(MyRegistrationDetailFacts{
		Item: item,
	}, now)
	if err != nil {
		t.Fatalf("ProjectMyRegistrationDetail(legacy contact) error = %v", err)
	}
	if detail.Contact != (MyRegistrationContact{}) {
		t.Fatalf("Contact = %+v", detail.Contact)
	}
}

func TestProjectMyRegistrationDetailMasksPhoneByLength(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 9, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(now)
	item, err := ProjectMyRegistration(facts, now)
	if err != nil {
		t.Fatalf("ProjectMyRegistration() error = %v", err)
	}
	for _, test := range []struct {
		phone string
		want  string
	}{
		{phone: "+12345678", want: "+1****78"},
		{phone: "+1234567890", want: "+1****90"},
		{phone: "+12345678901", want: "+123****8901"},
		{phone: "+8613812345678", want: "+861****5678"},
	} {
		test := test
		t.Run(test.phone, func(t *testing.T) {
			t.Parallel()
			detail, err := ProjectMyRegistrationDetail(MyRegistrationDetailFacts{
				Item: item,
				Contact: MyRegistrationContactFacts{
					Name:      "王薇",
					PhoneE164: test.phone,
				},
			}, now)
			if err != nil {
				t.Fatalf("ProjectMyRegistrationDetail() error = %v", err)
			}
			if detail.Contact.PhoneMasked != test.want {
				t.Fatalf("PhoneMasked = %q, want %q", detail.Contact.PhoneMasked, test.want)
			}
		})
	}
}

func validMyRegistrationContactFacts() MyRegistrationContactFacts {
	return MyRegistrationContactFacts{
		Name:      "王薇",
		PhoneE164: "+8613812345678",
	}
}
