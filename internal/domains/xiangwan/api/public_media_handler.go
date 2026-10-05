package xiangwanapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type publicMediaApplication interface {
	Open(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (*PublicMedia, error)
}

type PublicMediaHandler struct {
	service publicMediaApplication
}

func NewPublicMediaHandler(service publicMediaApplication) *PublicMediaHandler {
	return &PublicMediaHandler{service: service}
}

func (handler *PublicMediaHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET(
		"/media/:relation_id/:block_id/:file_id",
		handler.GetPublicMedia,
	)
	group.HEAD(
		"/media/:relation_id/:block_id/:file_id",
		handler.GetPublicMedia,
	)
}

// GetPublicMedia godoc
// @Summary Read one authorized Xiangwan public media object
// @Description Anonymous byte read fenced by the exact publication, Block, File, and active Runtime generation.
// @Tags xiangwan
// @Produce application/octet-stream
// @Param relation_id path string true "Resource relation ID"
// @Param block_id path string true "Published Block ID"
// @Param file_id path string true "Confirmed File ID"
// @Success 200 {file} binary
// @Failure 400 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/media/{relation_id}/{block_id}/{file_id} [get]
func (handler *PublicMediaHandler) GetPublicMedia(c *gin.Context) {
	if handler == nil || handler.service == nil {
		writeError(c, errx.NewInternal("xiangwan public media is unavailable"))
		return
	}
	relationID, relationErr := parseCanonicalUUID(c.Param("relation_id"))
	blockID, blockErr := parseCanonicalUUID(c.Param("block_id"))
	fileID, fileErr := parseCanonicalUUID(c.Param("file_id"))
	if relationErr != nil || blockErr != nil || fileErr != nil {
		writeError(c, errx.NewBadRequest("invalid media identity"))
		return
	}
	media, err := handler.service.Open(
		c.Request.Context(),
		relationID,
		blockID,
		fileID,
	)
	if err != nil {
		writePublicMediaError(c, err)
		return
	}
	if media == nil || media.Object == nil {
		writePublicMediaError(c, ErrPublicMediaFactsConflict)
		return
	}
	defer func() { _ = media.Object.Close() }()

	c.Header("Content-Type", media.Grant.MIME)
	c.Header("Content-Disposition", publicMediaDisposition(media.Grant.BlockType))
	http.ServeContent(
		c.Writer,
		c.Request,
		"",
		time.Time{},
		media.Object,
	)
}

func PublicMediaPath(
	relationID uuid.UUID,
	blockID uuid.UUID,
	fileID uuid.UUID,
) string {
	if relationID == uuid.Nil || blockID == uuid.Nil || fileID == uuid.Nil {
		return ""
	}
	return fmt.Sprintf(
		"/api/v1/xiangwan/media/%s/%s/%s",
		relationID,
		blockID,
		fileID,
	)
}

func publicMediaDisposition(blockType resource.PublicReviewBlockType) string {
	if blockType == resource.PublicReviewBlockTypeFile {
		return "attachment"
	}
	return "inline"
}

func writePublicMediaError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidPublicMediaRequest):
		writeError(c, errx.NewBadRequest("invalid media request"))
	case publicMediaNotFound(err):
		writeError(c, errx.NewNotFound("media not found"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan public media failed"))
	}
}
