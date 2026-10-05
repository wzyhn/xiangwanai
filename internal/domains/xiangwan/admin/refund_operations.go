package xiangwanadmin

import (
	"context"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/google/uuid"
)

type RefundAction string

const (
	RefundActionStart    RefundAction = "start"
	RefundActionFail     RefundAction = "fail"
	RefundActionComplete RefundAction = "complete"
	RefundActionReject   RefundAction = "reject"
)

type RefundActionCommand struct {
	ActorID               uuid.UUID
	IdentityLinkID        uuid.UUID
	OperationID           uuid.UUID
	CaseID                uuid.UUID
	ExpectedVersion       int64
	Action                RefundAction
	SuccessfulRefundCents int64
	ExternalRefundID      string
	EvidenceReference     string
	FailureReason         string
	OperatorNote          string
}

type RefundActionResult struct {
	CaseID                uuid.UUID
	Status                refund.Status
	Version               int64
	SuccessfulRefundCents int64
	EventSequence         int64
}

type RefundOperator interface {
	TransitionRefund(context.Context, RefundActionCommand) (RefundActionResult, error)
}
