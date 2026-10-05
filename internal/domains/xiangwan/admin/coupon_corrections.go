package xiangwanadmin

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// CouponCorrectionItem is an immutable exception marker, not a conclusion
// that an external financial or entitlement adjustment has been completed.
type CouponCorrectionItem struct {
	EntryID              uuid.UUID
	CouponID             uuid.UUID
	FaceValueCents       int64
	SourceCheckinEventID uuid.UUID
	RelatedEntryType     string
	OrderID              *uuid.UUID
	RegistrationID       *uuid.UUID
	RecordedAt           time.Time
	HandlingStatus       string
	HandlingVersion      int64
	EvidenceKind         string
	AdjustmentCents      int64
}

type CouponCorrectionFilter struct {
	Page     int
	PageSize int
	AsOf     *time.Time
}

type CouponCorrectionPage struct {
	Items    []CouponCorrectionItem
	Page     int
	PageSize int
	Total    int64
	AsOf     time.Time
}

type CouponCorrectionReader interface {
	ListCouponCorrections(context.Context, Principal, CouponCorrectionFilter) (CouponCorrectionPage, error)
}

type CouponCorrectionAction string

const (
	CouponCorrectionStart   CouponCorrectionAction = "start"
	CouponCorrectionResolve CouponCorrectionAction = "resolve"
)

// A resolution records a separately verified external financial adjustment or
// an entitlement closure already proved by the immutable Coupon ledger. It
// never changes the original Coupon, Order, or provider payment state.
type CouponCorrectionActionCommand struct {
	ActorID           uuid.UUID
	IdentityLinkID    uuid.UUID
	OperationID       uuid.UUID
	RequestID         string
	CorrectionEntryID uuid.UUID
	ExpectedVersion   int64
	Action            CouponCorrectionAction
	EvidenceKind      string
	EvidenceReference string
	AdjustmentCents   int64
	OperatorNote      string
}

type CouponCorrectionActionResult struct {
	CorrectionEntryID uuid.UUID
	CouponID          uuid.UUID
	Status            string
	Version           int64
	EvidenceKind      string
	AdjustmentCents   int64
	RecordedAt        time.Time
}

type CouponCorrectionOperator interface {
	TransitionCouponCorrection(context.Context, CouponCorrectionActionCommand) (CouponCorrectionActionResult, error)
}
