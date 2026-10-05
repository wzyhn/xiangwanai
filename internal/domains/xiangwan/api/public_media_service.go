package xiangwanapi

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/capabilities/storage/publicobject"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

var (
	ErrInvalidPublicMediaService = errors.New(
		"invalid xiangwan public media service",
	)
	ErrInvalidPublicMediaRequest = errors.New(
		"invalid xiangwan public media request",
	)
	ErrPublicMediaFactsConflict = errors.New(
		"xiangwan public media facts conflict",
	)
)

type publicFileAuthorizer interface {
	AuthorizePublicFile(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (resource.PublicFileGrant, error)
}

type PublicMediaService struct {
	tenantID   uuid.UUID
	authorizer publicFileAuthorizer
	objects    publicobject.Reader
}

type PublicMedia struct {
	Grant  resource.PublicFileGrant
	Object *publicobject.Object
}

func NewPublicMediaService(
	tenantID uuid.UUID,
	authorizer publicFileAuthorizer,
	objects publicobject.Reader,
) (*PublicMediaService, error) {
	if tenantID == uuid.Nil || authorizer == nil || objects == nil {
		return nil, ErrInvalidPublicMediaService
	}
	return &PublicMediaService{
		tenantID:   tenantID,
		authorizer: authorizer,
		objects:    objects,
	}, nil
}

func (service *PublicMediaService) Open(
	ctx context.Context,
	relationID uuid.UUID,
	blockID uuid.UUID,
	fileID uuid.UUID,
) (*PublicMedia, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.authorizer == nil || service.objects == nil {
		return nil, ErrInvalidPublicMediaService
	}
	if ctx == nil || relationID == uuid.Nil || blockID == uuid.Nil ||
		fileID == uuid.Nil {
		return nil, ErrInvalidPublicMediaRequest
	}
	grant, err := service.authorizer.AuthorizePublicFile(
		ctx,
		service.tenantID,
		relationID,
		blockID,
		fileID,
	)
	if err != nil {
		return nil, fmt.Errorf("authorize public media: %w", err)
	}
	if resource.ValidatePublicFileGrant(grant) != nil ||
		grant.TenantID != service.tenantID || grant.RelationID != relationID ||
		grant.BlockID != blockID || grant.FileID != fileID {
		return nil, ErrPublicMediaFactsConflict
	}
	object, err := service.objects.OpenConfirmedObject(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("open public media object: %w", err)
	}
	if object == nil || object.Content == nil || object.Closer == nil ||
		object.FileID != grant.FileID ||
		object.Size != grant.Size || object.MIME != grant.MIME ||
		!publicMediaTypeMatches(grant.BlockType, grant.MIME, object.DetectedMIME) {
		if object != nil {
			_ = object.Close()
		}
		return nil, ErrPublicMediaFactsConflict
	}
	return &PublicMedia{Grant: grant, Object: object}, nil
}

func publicMediaTypeMatches(
	blockType resource.PublicReviewBlockType,
	storedMIME string,
	detectedMIME string,
) bool {
	storedMIME = strings.ToLower(strings.TrimSpace(storedMIME))
	detectedMIME = strings.ToLower(strings.TrimSpace(detectedMIME))
	switch blockType {
	case resource.PublicReviewBlockTypeImage:
		return strings.HasPrefix(storedMIME, "image/") &&
			detectedMIME == storedMIME
	case resource.PublicReviewBlockTypeVideo:
		return strings.HasPrefix(storedMIME, "video/") &&
			detectedMIME == storedMIME
	case resource.PublicReviewBlockTypeAudio:
		return strings.HasPrefix(storedMIME, "audio/") &&
			detectedMIME == storedMIME
	case resource.PublicReviewBlockTypeFile:
		return storedMIME != "" && storedMIME != "text/html" &&
			storedMIME != "image/svg+xml" && detectedMIME != "text/html" &&
			detectedMIME != "image/svg+xml"
	default:
		return false
	}
}

func publicMediaNotFound(err error) bool {
	return errors.Is(err, resource.ErrPublicFileUnavailable) ||
		errors.Is(err, publicobject.ErrObjectNotFound)
}
