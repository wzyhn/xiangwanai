// Package coupon owns Xiangwan benefit instruments and their append-only ledger.
package coupon

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

const (
	GrantQuantity          = 5
	MaxGrantNarrativeRunes = 500
	MaxPolicyValidity      = 5 * 366 * 24 * time.Hour
)

type BenefitType string

const BenefitTypeRoundtableCoupon BenefitType = `roundtable_coupon`

type ScopeType string

const (
	ScopeTypeActivityType ScopeType = `activity_type`
	ScopeTypeSeries       ScopeType = `series`
)

type GrantKind string

const (
	GrantKindInitialGuest        GrantKind = `initial_guest`
	GrantKindManualReplenishment GrantKind = `manual_replenishment`
)

type EntryType string

const (
	EntryTypeGranted            EntryType = `granted`
	EntryTypeHeld               EntryType = `held`
	EntryTypeReleased           EntryType = `released`
	EntryTypeRedeemed           EntryType = `redeemed`
	EntryTypeRestored           EntryType = `restored`
	EntryTypeForfeited          EntryType = `forfeited`
	EntryTypeInvalidated        EntryType = `invalidated`
	EntryTypeCorrectionRequired EntryType = `correction_required`
)

type GrantPolicy struct {
	Configured        bool
	PolicyVersion     string
	FaceValueCents    int64
	Validity          time.Duration
	ScopeType         ScopeType
	ScopeActivityType *activity.ActivityType
	ScopeSeriesID     *uuid.UUID
	MinimumOrderCents int64
}

type SourceFacts struct {
	PeopleProfileID uuid.UUID
	PeopleBindingID uuid.UUID
	RoleBindingID   uuid.UUID
	CheckinID       uuid.UUID
	CheckinEventID  uuid.UUID
}

type Coupon struct {
	ID                  uuid.UUID
	TenantID            uuid.UUID
	PrincipalID         uuid.UUID
	BenefitType         BenefitType
	FaceValueCents      int64
	ScopeType           ScopeType
	ScopeActivityType   *activity.ActivityType
	ScopeSeriesID       *uuid.UUID
	MinimumOrderCents   int64
	ValidFrom           time.Time
	ExpiresAt           time.Time
	GrantKind           GrantKind
	GrantBusinessKey    string
	GrantOrdinal        int
	PolicyVersion       string
	SourcePeopleProfile *uuid.UUID
	SourcePeopleBinding *uuid.UUID
	SourceRoleBinding   *uuid.UUID
	SourceCheckin       *uuid.UUID
	SourceCheckinEvent  *uuid.UUID
	GrantedBy           *uuid.UUID
	GrantReason         *string
	GrantContext        *string
	GrantedAt           time.Time
	CreatedAt           time.Time
}

type Entry struct {
	ID                   uuid.UUID
	TenantID             uuid.UUID
	CouponID             uuid.UUID
	PrincipalID          uuid.UUID
	EntrySequence        int64
	EntryType            EntryType
	BusinessKey          string
	OrderID              *uuid.UUID
	RegistrationID       *uuid.UUID
	RelatedEntryID       *uuid.UUID
	RefundCaseID         *uuid.UUID
	SourceCheckinEventID *uuid.UUID
	ActorID              *uuid.UUID
	Reason               *string
	RefundPolicyVersion  *string
	OccurredAt           time.Time
	RecordedAt           time.Time
}

type Grant struct {
	Kind        GrantKind
	BusinessKey string
	Coupons     []Coupon
	Entries     []Entry
}

type InitialGuestGrantCommand struct {
	TenantID    uuid.UUID
	PrincipalID uuid.UUID
	Source      SourceFacts
	Policy      GrantPolicy
	CheckedInAt time.Time
	RecordedAt  time.Time
}

type ManualReplenishmentCommand struct {
	TenantID    uuid.UUID
	PrincipalID uuid.UUID
	ActorID     uuid.UUID
	BusinessKey string
	Reason      string
	Context     string
	Policy      GrantPolicy
	GrantedAt   time.Time
	RecordedAt  time.Time
}

var (
	ErrInvalidPolicy = errors.New(`invalid xiangwan Coupon grant policy`)
	ErrInvalidCoupon = errors.New(`invalid xiangwan Coupon`)
	ErrInvalidEntry  = errors.New(`invalid xiangwan Coupon ledger entry`)
	ErrInvalidGrant  = errors.New(`invalid xiangwan Coupon grant`)
)

var grantBusinessKeyPattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`,
)

func ValidateGrantPolicy(policy GrantPolicy) error {
	if !policy.Configured ||
		!validReference(policy.PolicyVersion) ||
		policy.FaceValueCents <= 0 ||
		policy.Validity <= 0 ||
		policy.Validity > MaxPolicyValidity ||
		policy.MinimumOrderCents < 0 {
		return ErrInvalidPolicy
	}
	switch policy.ScopeType {
	case ScopeTypeActivityType:
		if policy.ScopeActivityType == nil ||
			*policy.ScopeActivityType == activity.ActivityTypeAll ||
			!validActivityType(*policy.ScopeActivityType) ||
			policy.ScopeSeriesID != nil {
			return ErrInvalidPolicy
		}
	case ScopeTypeSeries:
		if policy.ScopeSeriesID == nil ||
			*policy.ScopeSeriesID == uuid.Nil ||
			policy.ScopeActivityType != nil {
			return ErrInvalidPolicy
		}
	default:
		return ErrInvalidPolicy
	}
	return nil
}

func NewInitialGuestGrant(
	command InitialGuestGrantCommand,
) (Grant, error) {
	if command.TenantID == uuid.Nil ||
		command.PrincipalID == uuid.Nil ||
		command.Source.PeopleProfileID == uuid.Nil ||
		command.Source.PeopleBindingID == uuid.Nil ||
		command.Source.RoleBindingID == uuid.Nil ||
		command.Source.CheckinID == uuid.Nil ||
		command.Source.CheckinEventID == uuid.Nil ||
		command.CheckedInAt.IsZero() ||
		command.RecordedAt.IsZero() ||
		command.RecordedAt.Before(command.CheckedInAt) ||
		ValidateGrantPolicy(command.Policy) != nil {
		return Grant{}, ErrInvalidGrant
	}
	return buildGrant(grantFacts{
		tenantID:    command.TenantID,
		principalID: command.PrincipalID,
		kind:        GrantKindInitialGuest,
		businessKey: `initial_guest_grant`,
		policy:      command.Policy,
		source:      &command.Source,
		grantedAt:   command.CheckedInAt.UTC(),
		recordedAt:  command.RecordedAt.UTC(),
	})
}

func NewManualReplenishment(
	command ManualReplenishmentCommand,
) (Grant, error) {
	reason := strings.TrimSpace(command.Reason)
	grantContext := strings.TrimSpace(command.Context)
	if command.TenantID == uuid.Nil ||
		command.PrincipalID == uuid.Nil ||
		command.ActorID == uuid.Nil ||
		!grantBusinessKeyPattern.MatchString(command.BusinessKey) ||
		reason == `` ||
		len([]rune(reason)) > MaxGrantNarrativeRunes ||
		grantContext == `` ||
		len([]rune(grantContext)) > MaxGrantNarrativeRunes ||
		command.GrantedAt.IsZero() ||
		command.RecordedAt.IsZero() ||
		command.RecordedAt.Before(command.GrantedAt) ||
		ValidateGrantPolicy(command.Policy) != nil {
		return Grant{}, ErrInvalidGrant
	}
	actorID := command.ActorID
	return buildGrant(grantFacts{
		tenantID:     command.TenantID,
		principalID:  command.PrincipalID,
		kind:         GrantKindManualReplenishment,
		businessKey:  command.BusinessKey,
		policy:       command.Policy,
		actorID:      &actorID,
		reason:       &reason,
		grantContext: &grantContext,
		grantedAt:    command.GrantedAt.UTC(),
		recordedAt:   command.RecordedAt.UTC(),
	})
}

type grantFacts struct {
	tenantID     uuid.UUID
	principalID  uuid.UUID
	kind         GrantKind
	businessKey  string
	policy       GrantPolicy
	source       *SourceFacts
	actorID      *uuid.UUID
	reason       *string
	grantContext *string
	grantedAt    time.Time
	recordedAt   time.Time
}

func buildGrant(facts grantFacts) (Grant, error) {
	result := Grant{
		Kind:        facts.kind,
		BusinessKey: facts.businessKey,
		Coupons:     make([]Coupon, 0, GrantQuantity),
		Entries:     make([]Entry, 0, GrantQuantity),
	}
	for ordinal := 1; ordinal <= GrantQuantity; ordinal++ {
		couponID := uuid.New()
		instrument := Coupon{
			ID:                couponID,
			TenantID:          facts.tenantID,
			PrincipalID:       facts.principalID,
			BenefitType:       BenefitTypeRoundtableCoupon,
			FaceValueCents:    facts.policy.FaceValueCents,
			ScopeType:         facts.policy.ScopeType,
			ScopeActivityType: cloneActivityType(facts.policy.ScopeActivityType),
			ScopeSeriesID:     cloneUUID(facts.policy.ScopeSeriesID),
			MinimumOrderCents: facts.policy.MinimumOrderCents,
			ValidFrom:         facts.grantedAt,
			ExpiresAt:         facts.grantedAt.Add(facts.policy.Validity),
			GrantKind:         facts.kind,
			GrantBusinessKey:  facts.businessKey,
			GrantOrdinal:      ordinal,
			PolicyVersion:     facts.policy.PolicyVersion,
			GrantedBy:         cloneUUID(facts.actorID),
			GrantReason:       cloneString(facts.reason),
			GrantContext:      cloneString(facts.grantContext),
			GrantedAt:         facts.grantedAt,
			CreatedAt:         facts.recordedAt,
		}
		if facts.source != nil {
			instrument.SourcePeopleProfile = cloneUUID(
				&facts.source.PeopleProfileID,
			)
			instrument.SourcePeopleBinding = cloneUUID(
				&facts.source.PeopleBindingID,
			)
			instrument.SourceRoleBinding = cloneUUID(
				&facts.source.RoleBindingID,
			)
			instrument.SourceCheckin = cloneUUID(&facts.source.CheckinID)
			instrument.SourceCheckinEvent = cloneUUID(
				&facts.source.CheckinEventID,
			)
		}
		entry := Entry{
			ID:            uuid.New(),
			TenantID:      facts.tenantID,
			CouponID:      couponID,
			PrincipalID:   facts.principalID,
			EntrySequence: 1,
			EntryType:     EntryTypeGranted,
			BusinessKey:   facts.businessKey,
			ActorID:       cloneUUID(facts.actorID),
			OccurredAt:    facts.grantedAt,
			RecordedAt:    facts.recordedAt,
		}
		if ValidateCoupon(instrument) != nil || ValidateEntry(entry) != nil {
			return Grant{}, ErrInvalidGrant
		}
		result.Coupons = append(result.Coupons, instrument)
		result.Entries = append(result.Entries, entry)
	}
	if err := ValidateGrant(result); err != nil {
		return Grant{}, err
	}
	return result, nil
}

func ValidateCoupon(value Coupon) error {
	if value.ID == uuid.Nil || value.TenantID == uuid.Nil ||
		value.PrincipalID == uuid.Nil ||
		value.BenefitType != BenefitTypeRoundtableCoupon ||
		value.FaceValueCents <= 0 || value.MinimumOrderCents < 0 ||
		value.ValidFrom.IsZero() || value.ExpiresAt.IsZero() ||
		!value.ExpiresAt.After(value.ValidFrom) ||
		!grantBusinessKeyPattern.MatchString(value.GrantBusinessKey) ||
		value.GrantOrdinal < 1 || value.GrantOrdinal > GrantQuantity ||
		!validReference(value.PolicyVersion) || value.GrantedAt.IsZero() ||
		value.CreatedAt.IsZero() || value.CreatedAt.Before(value.GrantedAt) ||
		!value.ValidFrom.Equal(value.GrantedAt) {
		return ErrInvalidCoupon
	}
	switch value.ScopeType {
	case ScopeTypeActivityType:
		if value.ScopeActivityType == nil ||
			!validActivityType(*value.ScopeActivityType) ||
			*value.ScopeActivityType == activity.ActivityTypeAll ||
			value.ScopeSeriesID != nil {
			return ErrInvalidCoupon
		}
	case ScopeTypeSeries:
		if value.ScopeSeriesID == nil ||
			*value.ScopeSeriesID == uuid.Nil ||
			value.ScopeActivityType != nil {
			return ErrInvalidCoupon
		}
	default:
		return ErrInvalidCoupon
	}
	switch value.GrantKind {
	case GrantKindInitialGuest:
		if value.GrantBusinessKey != `initial_guest_grant` ||
			value.SourcePeopleProfile == nil ||
			value.SourcePeopleBinding == nil ||
			value.SourceRoleBinding == nil ||
			value.SourceCheckin == nil ||
			value.SourceCheckinEvent == nil ||
			*value.SourcePeopleProfile == uuid.Nil ||
			*value.SourcePeopleBinding == uuid.Nil ||
			*value.SourceRoleBinding == uuid.Nil ||
			*value.SourceCheckin == uuid.Nil ||
			*value.SourceCheckinEvent == uuid.Nil ||
			value.GrantedBy != nil || value.GrantReason != nil ||
			value.GrantContext != nil {
			return ErrInvalidCoupon
		}
	case GrantKindManualReplenishment:
		if value.SourcePeopleProfile != nil ||
			value.SourcePeopleBinding != nil ||
			value.SourceRoleBinding != nil ||
			value.SourceCheckin != nil ||
			value.SourceCheckinEvent != nil ||
			value.GrantedBy == nil || *value.GrantedBy == uuid.Nil ||
			!validNarrative(value.GrantReason) ||
			!validNarrative(value.GrantContext) {
			return ErrInvalidCoupon
		}
	default:
		return ErrInvalidCoupon
	}
	return nil
}

func ValidateEntry(value Entry) error {
	if value.ID == uuid.Nil || value.TenantID == uuid.Nil ||
		value.CouponID == uuid.Nil || value.PrincipalID == uuid.Nil ||
		value.EntrySequence < 1 ||
		!grantBusinessKeyPattern.MatchString(value.BusinessKey) ||
		value.OccurredAt.IsZero() || value.RecordedAt.IsZero() ||
		value.RecordedAt.Before(value.OccurredAt) ||
		(value.OrderID != nil && *value.OrderID == uuid.Nil) ||
		(value.RegistrationID != nil && *value.RegistrationID == uuid.Nil) ||
		(value.RelatedEntryID != nil && *value.RelatedEntryID == uuid.Nil) ||
		(value.RefundCaseID != nil && *value.RefundCaseID == uuid.Nil) ||
		(value.SourceCheckinEventID != nil &&
			*value.SourceCheckinEventID == uuid.Nil) ||
		(value.ActorID != nil && *value.ActorID == uuid.Nil) {
		return ErrInvalidEntry
	}
	switch value.EntryType {
	case EntryTypeGranted:
		if value.EntrySequence != 1 || value.OrderID != nil ||
			value.RegistrationID != nil || value.RelatedEntryID != nil ||
			value.RefundCaseID != nil ||
			value.SourceCheckinEventID != nil || value.Reason != nil ||
			value.RefundPolicyVersion != nil {
			return ErrInvalidEntry
		}
	case EntryTypeHeld:
		if value.OrderID == nil || value.RegistrationID == nil ||
			value.RelatedEntryID != nil ||
			value.RefundCaseID != nil ||
			value.SourceCheckinEventID != nil || value.ActorID != nil ||
			value.Reason != nil || value.RefundPolicyVersion != nil {
			return ErrInvalidEntry
		}
	case EntryTypeReleased:
		if value.OrderID == nil || value.RegistrationID == nil ||
			value.RelatedEntryID == nil ||
			value.RefundCaseID != nil ||
			value.SourceCheckinEventID != nil || value.ActorID != nil ||
			!validNarrative(value.Reason) ||
			value.RefundPolicyVersion != nil {
			return ErrInvalidEntry
		}
	case EntryTypeRedeemed:
		if value.OrderID == nil || value.RegistrationID == nil ||
			value.RelatedEntryID == nil ||
			value.RefundCaseID != nil ||
			value.SourceCheckinEventID != nil || value.ActorID != nil ||
			value.Reason != nil || value.RefundPolicyVersion != nil {
			return ErrInvalidEntry
		}
	case EntryTypeRestored, EntryTypeForfeited:
		if value.OrderID == nil || value.RegistrationID == nil ||
			value.RelatedEntryID == nil ||
			value.SourceCheckinEventID != nil || value.ActorID == nil ||
			!validNarrative(value.Reason) ||
			value.RefundPolicyVersion == nil ||
			!refundPolicyVersionPattern.MatchString(*value.RefundPolicyVersion) {
			return ErrInvalidEntry
		}
	case EntryTypeInvalidated, EntryTypeCorrectionRequired:
		if value.OrderID != nil || value.RegistrationID != nil ||
			value.RelatedEntryID == nil ||
			value.RefundCaseID != nil ||
			value.SourceCheckinEventID == nil || value.ActorID == nil ||
			!validNarrative(value.Reason) ||
			value.RefundPolicyVersion != nil {
			return ErrInvalidEntry
		}
	default:
		return ErrInvalidEntry
	}
	return nil
}

func ValidateGrant(value Grant) error {
	if value.Kind != GrantKindInitialGuest &&
		value.Kind != GrantKindManualReplenishment {
		return ErrInvalidGrant
	}
	if !grantBusinessKeyPattern.MatchString(value.BusinessKey) ||
		len(value.Coupons) != GrantQuantity ||
		len(value.Entries) != GrantQuantity {
		return ErrInvalidGrant
	}
	couponIDs := make(map[uuid.UUID]struct{}, GrantQuantity)
	ordinals := make(map[int]struct{}, GrantQuantity)
	entriesByCoupon := make(map[uuid.UUID]Entry, GrantQuantity)
	for _, entry := range value.Entries {
		if ValidateEntry(entry) != nil ||
			entry.BusinessKey != value.BusinessKey {
			return ErrInvalidGrant
		}
		if _, duplicate := entriesByCoupon[entry.CouponID]; duplicate {
			return ErrInvalidGrant
		}
		entriesByCoupon[entry.CouponID] = entry
	}
	for _, instrument := range value.Coupons {
		if ValidateCoupon(instrument) != nil ||
			instrument.GrantKind != value.Kind ||
			instrument.GrantBusinessKey != value.BusinessKey {
			return ErrInvalidGrant
		}
		if _, duplicate := couponIDs[instrument.ID]; duplicate {
			return ErrInvalidGrant
		}
		couponIDs[instrument.ID] = struct{}{}
		if _, duplicate := ordinals[instrument.GrantOrdinal]; duplicate {
			return ErrInvalidGrant
		}
		ordinals[instrument.GrantOrdinal] = struct{}{}
		entry, exists := entriesByCoupon[instrument.ID]
		if !exists || entry.TenantID != instrument.TenantID ||
			entry.PrincipalID != instrument.PrincipalID ||
			entry.BusinessKey != instrument.GrantBusinessKey ||
			entry.ActorID == nil != (instrument.GrantedBy == nil) ||
			(entry.ActorID != nil && *entry.ActorID != *instrument.GrantedBy) ||
			!entry.OccurredAt.Equal(instrument.GrantedAt) ||
			!entry.RecordedAt.Equal(instrument.CreatedAt) {
			return ErrInvalidGrant
		}
	}
	return nil
}

func validActivityType(value activity.ActivityType) bool {
	switch value {
	case activity.ActivityTypeAIRoundtable,
		activity.ActivityTypeSpecialEvent,
		activity.ActivityTypeCourse,
		activity.ActivityTypeCompetition,
		activity.ActivityTypeCustom:
		return true
	default:
		return false
	}
}

func validReference(value string) bool {
	return grantBusinessKeyPattern.MatchString(value)
}

func validNarrative(value *string) bool {
	return value != nil && *value != `` &&
		*value == strings.TrimSpace(*value) &&
		len([]rune(*value)) <= MaxGrantNarrativeRunes
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneActivityType(
	value *activity.ActivityType,
) *activity.ActivityType {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
