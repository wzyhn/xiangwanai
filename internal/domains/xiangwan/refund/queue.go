package refund

import "github.com/google/uuid"

const (
	DefaultQueueLimit = 50
	MaxQueueLimit     = 100
)

// QueueFilter selects one actionable Refund state. Separate status tabs keep
// the PostgreSQL query aligned with the partial manual-queue index.
type QueueFilter struct {
	TenantID uuid.UUID
	Status   Status
	Limit    int
	Cursor   string
}

// QueueItem carries the current Activity labels needed by an operator to
// identify a Refund without making a second round of Activity lookups.
type QueueItem struct {
	Case          Case
	SeriesTitle   string
	InstanceTitle string
	SessionTitle  string
}

type QueuePage struct {
	Items        []QueueItem
	ActiveStatus Status
	NextCursor   string
}

// CaseDetail is a current Refund projection plus the immutable events visible
// at that projection's version.
type CaseDetail struct {
	Item   QueueItem
	Events []Event
}
