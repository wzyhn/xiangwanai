package coupon

import (
	"errors"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

const (
	DefaultMyCouponsLimit = 20
	MaxMyCouponsLimit     = 100
	MaxMyCouponsCursorAge = 30 * time.Minute
	MyCouponsFutureSkew   = 5 * time.Second
	MyCouponsCurrency     = "CNY"
)

type MyCouponState string

const (
	MyCouponStateAll                MyCouponState = "all"
	MyCouponStateAvailable          MyCouponState = "available"
	MyCouponStateHeld               MyCouponState = "held"
	MyCouponStateCorrectionRequired MyCouponState = "correction_required"
	MyCouponStateExpired            MyCouponState = "expired"
	MyCouponStateRedeemed           MyCouponState = "redeemed"
	MyCouponStateInvalidated        MyCouponState = "invalidated"
)

type MyCouponFilter struct {
	TenantID    uuid.UUID
	PrincipalID uuid.UUID
	State       MyCouponState
	Limit       int
	Cursor      string
	At          time.Time
}

type MyCouponApplicabilityTarget struct {
	ScopeType    ScopeType
	ActivityType *activity.ActivityType
	SeriesID     *uuid.UUID
}

type MyCouponAdjustment struct {
	EntryID       uuid.UUID
	EntryType     EntryType
	PolicyVersion string
	OccurredAt    time.Time
}

type MyCouponItem struct {
	CouponID             uuid.UUID
	FaceValueCents       int64
	Currency             string
	MinimumOrderCents    int64
	ValidFrom            time.Time
	ExpiresAt            time.Time
	GrantKind            GrantKind
	GrantPolicyVersion   string
	ApplicabilityTarget  MyCouponApplicabilityTarget
	State                MyCouponState
	LedgerStatus         Status
	Usable               bool
	CorrectionRequired   bool
	ActiveOrderID        *uuid.UUID
	ActiveRegistrationID *uuid.UUID
	LatestAdjustment     *MyCouponAdjustment
	LastEntryID          uuid.UUID
	LastEntrySequence    int64
	LastEntryType        EntryType
	LastBusinessAt       time.Time
	SortRank             int
}

type MyCouponsPage struct {
	Items       []MyCouponItem
	ActiveState MyCouponState
	AsOf        time.Time
	NextCursor  string
}

var ErrInvalidMyCouponFacts = errors.New("invalid xiangwan My Coupon facts")

func ProjectMyCoupon(
	instrument Coupon,
	history []Entry,
	at time.Time,
) (MyCouponItem, error) {
	projection, err := Project(instrument, history, at)
	if err != nil {
		return MyCouponItem{}, ErrInvalidMyCouponFacts
	}
	state, err := projectMyCouponState(projection)
	if err != nil {
		return MyCouponItem{}, err
	}
	target, err := projectMyCouponApplicabilityTarget(instrument)
	if err != nil {
		return MyCouponItem{}, err
	}
	lastEntry := history[len(history)-1]
	item := MyCouponItem{
		CouponID:            instrument.ID,
		FaceValueCents:      instrument.FaceValueCents,
		Currency:            MyCouponsCurrency,
		MinimumOrderCents:   instrument.MinimumOrderCents,
		ValidFrom:           instrument.ValidFrom.UTC(),
		ExpiresAt:           instrument.ExpiresAt.UTC(),
		GrantKind:           instrument.GrantKind,
		GrantPolicyVersion:  instrument.PolicyVersion,
		ApplicabilityTarget: target,
		State:               state,
		LedgerStatus:        projection.Status,
		Usable:              state == MyCouponStateAvailable,
		CorrectionRequired:  projection.CorrectionRequired,
		LastEntryID:         lastEntry.ID,
		LastEntrySequence:   lastEntry.EntrySequence,
		LastEntryType:       lastEntry.EntryType,
		LastBusinessAt:      lastEntry.OccurredAt.UTC(),
		SortRank:            MyCouponStateSortRank(state),
	}
	if projection.ActiveHold != nil {
		item.ActiveOrderID = cloneUUID(projection.ActiveHold.OrderID)
		item.ActiveRegistrationID = cloneUUID(
			projection.ActiveHold.RegistrationID,
		)
	}
	if projection.RefundAdjustment != nil {
		adjustment := projection.RefundAdjustment
		if adjustment.RefundPolicyVersion == nil {
			return MyCouponItem{}, ErrInvalidMyCouponFacts
		}
		item.LatestAdjustment = &MyCouponAdjustment{
			EntryID:       adjustment.ID,
			EntryType:     adjustment.EntryType,
			PolicyVersion: *adjustment.RefundPolicyVersion,
			OccurredAt:    adjustment.OccurredAt.UTC(),
		}
	}
	return item, nil
}

func ValidMyCouponState(value MyCouponState) bool {
	switch value {
	case MyCouponStateAll,
		MyCouponStateAvailable,
		MyCouponStateHeld,
		MyCouponStateCorrectionRequired,
		MyCouponStateExpired,
		MyCouponStateRedeemed,
		MyCouponStateInvalidated:
		return true
	default:
		return false
	}
}

func MyCouponStateSortRank(value MyCouponState) int {
	switch value {
	case MyCouponStateAvailable:
		return 1
	case MyCouponStateHeld:
		return 2
	case MyCouponStateCorrectionRequired:
		return 3
	case MyCouponStateExpired:
		return 4
	case MyCouponStateRedeemed:
		return 5
	case MyCouponStateInvalidated:
		return 6
	default:
		return 0
	}
}

func projectMyCouponState(
	projection Projection,
) (MyCouponState, error) {
	if projection.CorrectionRequired {
		return MyCouponStateCorrectionRequired, nil
	}
	switch projection.Status {
	case StatusAvailable:
		return MyCouponStateAvailable, nil
	case StatusHeld:
		return MyCouponStateHeld, nil
	case StatusRedeemed:
		return MyCouponStateRedeemed, nil
	case StatusInvalidated:
		return MyCouponStateInvalidated, nil
	case StatusExpired:
		return MyCouponStateExpired, nil
	default:
		return "", ErrInvalidMyCouponFacts
	}
}

func projectMyCouponApplicabilityTarget(
	instrument Coupon,
) (MyCouponApplicabilityTarget, error) {
	switch instrument.ScopeType {
	case ScopeTypeActivityType:
		if instrument.ScopeActivityType == nil ||
			instrument.ScopeSeriesID != nil {
			return MyCouponApplicabilityTarget{}, ErrInvalidMyCouponFacts
		}
		return MyCouponApplicabilityTarget{
			ScopeType:    ScopeTypeActivityType,
			ActivityType: cloneActivityType(instrument.ScopeActivityType),
		}, nil
	case ScopeTypeSeries:
		if instrument.ScopeSeriesID == nil ||
			instrument.ScopeActivityType != nil {
			return MyCouponApplicabilityTarget{}, ErrInvalidMyCouponFacts
		}
		return MyCouponApplicabilityTarget{
			ScopeType: ScopeTypeSeries,
			SeriesID:  cloneUUID(instrument.ScopeSeriesID),
		}, nil
	default:
		return MyCouponApplicabilityTarget{}, ErrInvalidMyCouponFacts
	}
}
