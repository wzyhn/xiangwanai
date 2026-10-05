package xiangwanadmin

import (
	"context"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

// SeriesManagementCatalog is the optional administrator surface for editing
// long-lived Series facts.  It stays separate from Catalog so existing
// read-only test fakes and integrations do not gain a write method by
// accident.  Instance titles are immutable snapshots; changing a Series title
// affects only future periods created from the Series.
type SeriesManagementCatalog interface {
	UpdateSeries(context.Context, UpdateSeriesCommand) (activity.Series, error)
}

// UpdateSeriesCommand patches the stable Series title and homepage exposure.
// A nil pointer leaves the corresponding field unchanged.  The expected
// version fences the write and the operation identity makes retries converge
// to the original receipt.
type UpdateSeriesCommand struct {
	ActorID         uuid.UUID
	IdentityLinkID  uuid.UUID
	OperationID     uuid.UUID
	RequestID       string
	SeriesID        uuid.UUID
	ExpectedVersion int64
	Title           *string
	HomeVisible     *bool
}
