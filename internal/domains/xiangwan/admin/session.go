package xiangwanadmin

import (
	"context"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

// SessionManagementCatalog is the draft-session editor surface. Published or
// terminal Session facts stay immutable; the concrete PostgreSQL catalog
// applies this command only while the parent Instance is still a draft.
type SessionManagementCatalog interface {
	UpdateSession(context.Context, UpdateSessionCommand) (activity.Session, error)
}

// UpdateSessionCommand replaces the complete operational shape of one draft
// Session. Both the Session and parent Instance versions fence the write so a
// stale editor cannot overwrite a newer schedule or capacity change.
type UpdateSessionCommand struct {
	ActorID                 uuid.UUID
	IdentityLinkID          uuid.UUID
	OperationID             uuid.UUID
	RequestID               string
	InstanceID              uuid.UUID
	SessionID               uuid.UUID
	ExpectedInstanceVersion int64
	ExpectedSessionVersion  int64
	Title                   string
	RegistrationStartAt     time.Time
	RegistrationEndAt       time.Time
	SessionStartAt          time.Time
	SessionEndAt            time.Time
	Capacity                int
	GroupMinimum            int
	LowStockThreshold       int
	PriceCents              int64
	DeliveryMode            activity.DeliveryMode
	Area                    activity.AreaCode
	VenueName               string
	Address                 string
	Longitude               *float64
	Latitude                *float64
	OnlineParticipationMode string
	OnlineCompliant         bool
	SortOrder               int
}
