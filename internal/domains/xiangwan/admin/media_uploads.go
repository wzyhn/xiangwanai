package xiangwanadmin

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidMediaUpload  = errors.New("invalid xiangwan review media upload")
	ErrMediaUploadConflict = errors.New("xiangwan review media upload conflict")
	ErrMediaUploadExpired  = errors.New("xiangwan review media upload expired")
)

type ReviewMediaKind string

const (
	ReviewMediaPhoto    ReviewMediaKind = "photo"
	ReviewMediaVideo    ReviewMediaKind = "video"
	ReviewMediaAudio    ReviewMediaKind = "audio"
	ReviewMediaMaterial ReviewMediaKind = "material"
)

type IssueReviewMediaUploadCommand struct {
	OperationID uuid.UUID
	InstanceID  uuid.UUID
	SessionID   *uuid.UUID
	Kind        ReviewMediaKind
	MIME        string
	Size        int64
	SHA256      string
}

type ReviewMediaUpload struct {
	FileID      uuid.UUID
	InstanceID  uuid.UUID
	SessionID   *uuid.UUID
	Kind        ReviewMediaKind
	MIME        string
	Size        int64
	SHA256      string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	ConfirmedAt *time.Time
}

// ReviewMediaPreview is a private, one-request stream of the exact confirmed
// bytes. It never includes a filesystem path or public Storage URL.
type ReviewMediaPreview struct {
	Reader io.ReadSeekCloser
	MIME   string
	Size   int64
}

type ReviewMediaUploadService interface {
	IssueReviewMediaUpload(context.Context, Principal, IssueReviewMediaUploadCommand) (ReviewMediaUpload, error)
	StageReviewMediaBytes(context.Context, Principal, uuid.UUID, io.Reader) (ReviewMediaUpload, error)
	ConfirmReviewMediaUpload(context.Context, Principal, uuid.UUID, string) (ReviewMediaUpload, error)
	OpenReviewMediaPreview(context.Context, Principal, uuid.UUID) (ReviewMediaPreview, error)
}
