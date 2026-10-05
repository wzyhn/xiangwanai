package resource

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// PhotoCuration changes only the public presentation of image Blocks from an
// immutable, already-published review relation. An omitted Block is hidden;
// the ordered list can later include it again without changing the Content.
type PhotoCurationReadCommand struct {
	ActorID        uuid.UUID
	IdentityLinkID uuid.UUID
	RelationID     uuid.UUID
	RequestID      string
}

type PhotoCurationListCommand struct {
	ActorID        uuid.UUID
	IdentityLinkID uuid.UUID
	InstanceID     uuid.UUID
	RequestID      string
}

type PhotoCurationWriteCommand struct {
	PhotoCurationReadCommand
	OperationID     uuid.UUID
	ExpectedVersion int64
	OrderedBlockIDs []uuid.UUID
	CoverBlockID    *uuid.UUID
	// CoverSpecified preserves old clients: omitted cover commands retain an
	// existing selected cover if it remains visible. Explicit nil means automatic.
	CoverSpecified bool
}

type ReviewPhoto struct {
	BlockID uuid.UUID
	URL     string
	FileID  *uuid.UUID
}

type PhotoCurationView struct {
	RelationID      uuid.UUID
	InstanceID      uuid.UUID
	SessionID       *uuid.UUID
	Title           string
	Version         int64
	OriginalPhotos  []ReviewPhoto
	OrderedBlockIDs []uuid.UUID
	CoverBlockID    *uuid.UUID
}

type PhotoCurationReceipt struct {
	RelationID      uuid.UUID
	Version         int64
	OrderedBlockIDs []uuid.UUID
	CoverBlockID    *uuid.UUID
}

type PhotoCurationService interface {
	ListPhotoCurations(context.Context, PhotoCurationListCommand) ([]PhotoCurationView, error)
	ReadPhotoCuration(context.Context, PhotoCurationReadCommand) (PhotoCurationView, error)
	WritePhotoCuration(context.Context, PhotoCurationWriteCommand) (PhotoCurationReceipt, error)
}

var (
	ErrInvalidPhotoCuration  = errors.New("invalid review photo curation")
	ErrPhotoCurationNotFound = errors.New("published review photo relation not found")
	ErrPhotoCurationConflict = errors.New("review photo curation changed")
)
