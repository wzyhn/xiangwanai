package resource

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// CreateReviewResourceCommand is the administrator command for one immutable
// public review document. The implementation owns the Content and resource
// publication transaction; callers provide only the exact activity target and
// the already authenticated administrator identity.
type CreateReviewResourceCommand struct {
	ActorID uuid.UUID
	// IdentityLinkID is the exact administrator identity link that was
	// authenticated for this request.  The PostgreSQL writer rechecks this
	// binding, the live Principal, Grant, and active runtime generation in the
	// same transaction as the Content/resource write.
	IdentityLinkID        uuid.UUID
	InstanceID            uuid.UUID
	SessionID             *uuid.UUID
	ExpectedTargetVersion int64
	// ReplacesRelationID appends a new approved document for the same target.
	// Photo curation is carried forward only at the exact version read by the editor.
	ReplacesRelationID           *uuid.UUID
	ExpectedPhotoCurationVersion *int64
	Title                        string
	Description                  string
	VideoURL                     string
	VideoChannel                 *ReviewVideoChannel
	// Photos are administrator-selected HTTPS image references. They are
	// canonicalized against the runtime external-domain policy by the writer;
	// raw bytes and arbitrary URLs never enter this command.
	Photos []string
	// Files are immutable, confirmed platform File references selected after
	// private byte review. The writer verifies each exact upload intent and
	// checksum, then pins the File in its publication transaction. A caller
	// cannot supply a provider key, path, filename, MIME or public URL.
	Files []ReviewResourceFile
	// Links contains optional operator-authored resources such as recording
	// notes or activity materials. The writer pins the canonical URL inside
	// the same immutable Content revision as the review body.
	Links       []ReviewResourceLink
	SortOrder   int
	OperationID uuid.UUID
	RequestID   string
	Now         time.Time
}

// ReviewResourceLink is a small, typed link projection for the two resource
// rows shown by the Xiangwan prototype. Kind is intentionally closed so a
// caller cannot smuggle an arbitrary content type into the public review.
type ReviewResourceLink struct {
	Hidden   bool
	Kind     string
	Title    string
	Subtitle string
	URL      string
}

type ReviewResourceListCommand struct {
	ActorID        uuid.UUID
	IdentityLinkID uuid.UUID
	InstanceID     uuid.UUID
	RequestID      string
}

// ReviewResourceEditView is administrator-only; hidden links never enter the
// public projection. Files are not editable through the HTTPS resource form.
type ReviewResourceEditView struct {
	RelationID            uuid.UUID
	SessionID             *uuid.UUID
	ExpectedTargetVersion int64
	PhotoCurationVersion  int64
	Title                 string
	Description           string
	VideoURL              string
	VideoChannel          *ReviewVideoChannel
	Photos                []string
	Links                 []ReviewResourceLink
	SortOrder             int
	Editable              bool
}

type ReviewResourceEditor interface {
	ListReviewResources(context.Context, ReviewResourceListCommand) ([]ReviewResourceEditView, error)
}

type ReviewResourceFile struct {
	FileID   uuid.UUID
	Kind     string
	SHA256   string
	Reviewed bool
}

const (
	ReviewResourceFilePhoto    = "photo"
	ReviewResourceFileVideo    = "video"
	ReviewResourceFileAudio    = "audio"
	ReviewResourceFileMaterial = "material"
)

const (
	ReviewResourceLinkRecording = "recording"
	ReviewResourceLinkMaterials = "materials"
)

// ReviewResourceReceipt is the durable receipt returned after the Content,
// relation, snapshot, moderation observation and publication facts commit.
type ReviewResourceReceipt struct {
	RelationID    uuid.UUID
	PublicationID uuid.UUID
	ContentID     uuid.UUID
	TenantID      uuid.UUID
	InstanceID    uuid.UUID
	SessionID     *uuid.UUID
	Title         string
	PublishedAt   time.Time
}

// ReviewResourceWriter is deliberately small so the API package cannot reach
// shared Content or File tables directly. Implementations must keep all facts
// in one caller-independent PostgreSQL transaction, recheck the exact
// administrator identity/generation binding, and return a durable idempotent
// receipt.
type ReviewResourceWriter interface {
	CreateReviewResource(context.Context, CreateReviewResourceCommand) (ReviewResourceReceipt, error)
}

var (
	ErrInvalidReviewResourceCommand = errors.New("invalid xiangwan review resource command")
	ErrReviewResourceExternalLink   = errors.New("xiangwan review external link is not allowed")
	ErrReviewResourceConflict       = errors.New("xiangwan review resource conflicts with an existing fact")
	ErrReviewResourceUnavailable    = errors.New("xiangwan review resource writer is unavailable")
)
