package xiangwanadmin

import (
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"context"
	"github.com/google/uuid"
)

type RevokeCheckinCommand struct {
	ActorID         uuid.UUID
	IdentityLinkID  uuid.UUID
	OperationID     uuid.UUID
	RequestID       string
	RegistrationID  uuid.UUID
	CheckinID       uuid.UUID
	ExpectedVersion int64
	Reason          string
}

type CheckinRevocationResult struct {
	Checkin   checkin.Checkin
	EventID   uuid.UUID
	Duplicate bool
}

type CheckinAdministration interface {
	GetRegistrationCheckin(context.Context, Principal, uuid.UUID, string) (checkin.Checkin, error)
	RevokeRegistrationCheckin(context.Context, RevokeCheckinCommand) (CheckinRevocationResult, error)
}
