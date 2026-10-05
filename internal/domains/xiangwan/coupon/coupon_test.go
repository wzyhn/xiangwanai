package coupon

import (
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestValidActivityTypeAcceptsCustomActivity(t *testing.T) {
	t.Parallel()
	if !validActivityType(activity.ActivityTypeCustom) {
		t.Fatal("custom activity type was rejected for activity-scoped coupons")
	}
}

func TestNewInitialGuestGrantCreatesExactlyFiveAuditableCoupons(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	source := SourceFacts{
		PeopleProfileID: uuid.New(),
		PeopleBindingID: uuid.New(),
		RoleBindingID:   uuid.New(),
		CheckinID:       uuid.New(),
		CheckinEventID:  uuid.New(),
	}
	grant, err := NewInitialGuestGrant(InitialGuestGrantCommand{
		TenantID:    uuid.New(),
		PrincipalID: uuid.New(),
		Source:      source,
		Policy:      configuredPolicy(),
		CheckedInAt: now,
		RecordedAt:  now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`NewInitialGuestGrant() error = %v`, err)
	}
	if grant.Kind != GrantKindInitialGuest ||
		grant.BusinessKey != `initial_guest_grant` ||
		len(grant.Coupons) != GrantQuantity ||
		len(grant.Entries) != GrantQuantity {
		t.Fatalf(`NewInitialGuestGrant() = %+v`, grant)
	}
	seenCouponIDs := make(map[uuid.UUID]struct{}, GrantQuantity)
	for index, instrument := range grant.Coupons {
		if instrument.GrantOrdinal != index+1 ||
			instrument.SourceCheckin == nil ||
			*instrument.SourceCheckin != source.CheckinID ||
			instrument.SourceRoleBinding == nil ||
			*instrument.SourceRoleBinding != source.RoleBindingID ||
			instrument.GrantedBy != nil ||
			instrument.FaceValueCents != 5000 ||
			!instrument.ExpiresAt.Equal(now.Add(90*24*time.Hour)) {
			t.Fatalf(`coupon[%d] = %+v`, index, instrument)
		}
		if _, duplicate := seenCouponIDs[instrument.ID]; duplicate {
			t.Fatalf(`duplicate coupon id %s`, instrument.ID)
		}
		seenCouponIDs[instrument.ID] = struct{}{}
		if grant.Entries[index].CouponID != instrument.ID ||
			grant.Entries[index].ActorID != nil {
			t.Fatalf(`entry[%d] = %+v`, index, grant.Entries[index])
		}
	}
	if err := ValidateGrant(grant); err != nil {
		t.Fatalf(`ValidateGrant() error = %v`, err)
	}
}

func TestNewManualReplenishmentCapturesOperatorReasonAndContext(
	t *testing.T,
) {
	t.Parallel()

	now := time.Now().UTC()
	actorID := uuid.New()
	grant, err := NewManualReplenishment(ManualReplenishmentCommand{
		TenantID:    uuid.New(),
		PrincipalID: uuid.New(),
		ActorID:     actorID,
		BusinessKey: `manual:case-2027-001`,
		Reason:      `  prior coupon balance is exhausted  `,
		Context:     `  support-case-2027-001  `,
		Policy:      configuredPolicy(),
		GrantedAt:   now,
		RecordedAt:  now,
	})
	if err != nil {
		t.Fatalf(`NewManualReplenishment() error = %v`, err)
	}
	for index, instrument := range grant.Coupons {
		if instrument.GrantKind != GrantKindManualReplenishment ||
			instrument.GrantedBy == nil ||
			*instrument.GrantedBy != actorID ||
			instrument.GrantReason == nil ||
			*instrument.GrantReason != `prior coupon balance is exhausted` ||
			instrument.GrantContext == nil ||
			*instrument.GrantContext != `support-case-2027-001` ||
			instrument.SourceCheckin != nil ||
			grant.Entries[index].ActorID == nil ||
			*grant.Entries[index].ActorID != actorID {
			t.Fatalf(`manual coupon/entry[%d] = %+v %+v`, index, instrument, grant.Entries[index])
		}
	}
}

func TestCouponPolicyRequiresSignedFaceExpiryAndScope(t *testing.T) {
	t.Parallel()

	valid := configuredPolicy()
	invalid := []GrantPolicy{
		{},
		func() GrantPolicy { value := valid; value.Configured = false; return value }(),
		func() GrantPolicy { value := valid; value.FaceValueCents = 0; return value }(),
		func() GrantPolicy { value := valid; value.Validity = 0; return value }(),
		func() GrantPolicy { value := valid; value.ScopeActivityType = nil; return value }(),
		func() GrantPolicy {
			value := valid
			seriesID := uuid.New()
			value.ScopeSeriesID = &seriesID
			return value
		}(),
	}
	for index, policy := range invalid {
		if err := ValidateGrantPolicy(policy); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf(`ValidateGrantPolicy(case %d) error = %v`, index, err)
		}
	}
}

func TestCouponGrantRejectsIncompleteEvidenceOrAudit(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	if _, err := NewInitialGuestGrant(InitialGuestGrantCommand{
		TenantID:    uuid.New(),
		PrincipalID: uuid.New(),
		Policy:      configuredPolicy(),
		CheckedInAt: now,
		RecordedAt:  now,
	}); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf(`NewInitialGuestGrant(incomplete) error = %v`, err)
	}
	if _, err := NewManualReplenishment(ManualReplenishmentCommand{
		TenantID:    uuid.New(),
		PrincipalID: uuid.New(),
		ActorID:     uuid.New(),
		BusinessKey: `manual:bad`,
		Policy:      configuredPolicy(),
		GrantedAt:   now,
		RecordedAt:  now,
	}); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf(`NewManualReplenishment(incomplete) error = %v`, err)
	}
}

func configuredPolicy() GrantPolicy {
	activityType := activity.ActivityTypeAIRoundtable
	return GrantPolicy{
		Configured:        true,
		PolicyVersion:     `roundtable-coupon-v1`,
		FaceValueCents:    5000,
		Validity:          90 * 24 * time.Hour,
		ScopeType:         ScopeTypeActivityType,
		ScopeActivityType: &activityType,
		MinimumOrderCents: 5000,
	}
}
