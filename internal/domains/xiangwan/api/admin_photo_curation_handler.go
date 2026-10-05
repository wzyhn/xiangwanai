package xiangwanapi

import (
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListReviewPhotoCurations godoc
// @Summary List published Instance and Session review documents including hidden photos
// @Tags xiangwan-admin
// @Produce json
// @Param instance_id path string true "Instance UUID"
// @Success 200 {object} AdminPhotoCurationPageResponse
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/review-photo-curations [get]
func (handler *AdminCatalogHandler) ListReviewPhotoCurations(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c) {
		return
	}
	if handler.photoCuration == nil {
		writeError(c, fmt.Errorf("photo curation service is unavailable"))
		return
	}
	instanceID, err := parseCanonicalUUID(c.Param("instance_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Instance ID"))
		return
	}
	views, err := handler.photoCuration.ListPhotoCurations(c.Request.Context(), resource.PhotoCurationListCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		InstanceID: instanceID, RequestID: adminRequestID(c),
	})
	if err != nil {
		writeAdminPhotoCurationError(c, err)
		return
	}
	items := make([]AdminPhotoCurationResponse, 0, len(views))
	for _, view := range views {
		items = append(items, projectAdminPhotoCuration(view))
	}
	response.OK(c, AdminPhotoCurationPageResponse{Items: items})
}

// GetReviewPhotoCuration godoc
// @Summary Read all original photos and the current public order for a published review
// @Tags xiangwan-admin
// @Produce json
// @Param relation_id path string true "Published review relation UUID"
// @Success 200 {object} AdminPhotoCurationResponse
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Router /xiangwan/admin/review-resources/{relation_id}/photo-curation [get]
func (handler *AdminCatalogHandler) GetReviewPhotoCuration(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c) {
		return
	}
	if handler.photoCuration == nil {
		writeError(c, fmt.Errorf("photo curation service is unavailable"))
		return
	}
	relationID, err := parseCanonicalUUID(c.Param("relation_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid review relation ID"))
		return
	}
	view, err := handler.photoCuration.ReadPhotoCuration(c.Request.Context(), resource.PhotoCurationReadCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		RelationID: relationID, RequestID: adminRequestID(c),
	})
	if err != nil {
		writeAdminPhotoCurationError(c, err)
		return
	}
	response.OK(c, projectAdminPhotoCuration(view))
}

// WriteReviewPhotoCuration godoc
// @Summary Hide or reorder already-published review photos and select their cover
// @Description ADMIN OP-KEY command. Appends a versioned presentation choice without mutating approved Content or publication. Omitted image Blocks become hidden in public detail, preview and file access. cover_block_id chooses a visible image; an empty string restores automatic selection. Omission or null retains a still-visible cover for older clients.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param relation_id path string true "Published review relation UUID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body AdminPhotoCurationRequest true "Exact curation version and ordered visible image Block IDs"
// @Success 200 {object} AdminPhotoCurationReceiptResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/review-resources/{relation_id}/photo-curation [post]
func (handler *AdminCatalogHandler) WriteReviewPhotoCuration(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if handler.photoCuration == nil {
		writeError(c, fmt.Errorf("photo curation service is unavailable"))
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	relationID, err := parseCanonicalUUID(c.Param("relation_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid review relation ID"))
		return
	}
	var payload AdminPhotoCurationRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	if payload.ExpectedVersion < 0 || payload.OrderedBlockIDs == nil || len(payload.OrderedBlockIDs) > 30 {
		writeError(c, errx.NewBadRequest("invalid photo curation"))
		return
	}
	ids := make([]uuid.UUID, 0, len(payload.OrderedBlockIDs))
	for _, raw := range payload.OrderedBlockIDs {
		id, err := parseCanonicalUUID(raw)
		if err != nil {
			writeError(c, errx.NewBadRequest("invalid photo Block ID"))
			return
		}
		ids = append(ids, id)
	}
	var cover *uuid.UUID
	if payload.CoverBlockID != nil && *payload.CoverBlockID != "" {
		id, err := parseCanonicalUUID(*payload.CoverBlockID)
		if err != nil {
			writeError(c, errx.NewBadRequest("invalid cover photo Block ID"))
			return
		}
		cover = &id
	}
	receipt, err := handler.photoCuration.WritePhotoCuration(c.Request.Context(), resource.PhotoCurationWriteCommand{
		PhotoCurationReadCommand: resource.PhotoCurationReadCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			RelationID: relationID, RequestID: adminRequestID(c),
		},
		OperationID: operationID, ExpectedVersion: payload.ExpectedVersion,
		OrderedBlockIDs: ids, CoverBlockID: cover, CoverSpecified: payload.CoverBlockID != nil,
	})
	if err != nil {
		writeAdminPhotoCurationError(c, err)
		return
	}
	result := AdminPhotoCurationReceiptResponse{
		RelationID: receipt.RelationID.String(), Version: receipt.Version,
		OrderedBlockIDs: make([]string, 0, len(receipt.OrderedBlockIDs)),
	}
	if receipt.CoverBlockID != nil {
		result.CoverBlockID = receipt.CoverBlockID.String()
	}
	for _, id := range receipt.OrderedBlockIDs {
		result.OrderedBlockIDs = append(result.OrderedBlockIDs, id.String())
	}
	response.OK(c, result)
}

type AdminPhotoCurationRequest struct {
	ExpectedVersion int64    `json:"expected_version"`
	OrderedBlockIDs []string `json:"ordered_block_ids"`
	CoverBlockID    *string  `json:"cover_block_id"`
}

type AdminPhotoCurationPhotoResponse struct {
	BlockID string `json:"block_id"`
	URL     string `json:"url,omitempty"`
	FileID  string `json:"file_id,omitempty"`
}

type AdminPhotoCurationResponse struct {
	RelationID      string                            `json:"relation_id"`
	InstanceID      string                            `json:"instance_id"`
	SessionID       string                            `json:"session_id,omitempty"`
	Title           string                            `json:"title"`
	Version         int64                             `json:"version"`
	OriginalPhotos  []AdminPhotoCurationPhotoResponse `json:"original_photos"`
	OrderedBlockIDs []string                          `json:"ordered_block_ids"`
	CoverBlockID    string                            `json:"cover_block_id,omitempty"`
}

type AdminPhotoCurationPageResponse struct {
	Items []AdminPhotoCurationResponse `json:"items"`
}

type AdminPhotoCurationReceiptResponse struct {
	RelationID      string   `json:"relation_id"`
	Version         int64    `json:"version"`
	OrderedBlockIDs []string `json:"ordered_block_ids"`
	CoverBlockID    string   `json:"cover_block_id,omitempty"`
}

func projectAdminPhotoCuration(view resource.PhotoCurationView) AdminPhotoCurationResponse {
	result := AdminPhotoCurationResponse{
		RelationID: view.RelationID.String(), InstanceID: view.InstanceID.String(),
		Title: view.Title, Version: view.Version,
		OriginalPhotos:  make([]AdminPhotoCurationPhotoResponse, 0, len(view.OriginalPhotos)),
		OrderedBlockIDs: make([]string, 0, len(view.OrderedBlockIDs)),
	}
	if view.SessionID != nil {
		result.SessionID = view.SessionID.String()
	}
	if view.CoverBlockID != nil {
		result.CoverBlockID = view.CoverBlockID.String()
	}
	for _, photo := range view.OriginalPhotos {
		item := AdminPhotoCurationPhotoResponse{BlockID: photo.BlockID.String(), URL: photo.URL}
		if photo.FileID != nil {
			item.FileID = photo.FileID.String()
		}
		result.OriginalPhotos = append(result.OriginalPhotos, item)
	}
	for _, id := range view.OrderedBlockIDs {
		result.OrderedBlockIDs = append(result.OrderedBlockIDs, id.String())
	}
	return result
}

func writeAdminPhotoCurationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, resource.ErrInvalidPhotoCuration):
		writeError(c, errx.NewBadRequest("invalid photo curation"))
	case errors.Is(err, resource.ErrPhotoCurationNotFound):
		writeError(c, errx.NewNotFound("review photos not found"))
	case errors.Is(err, resource.ErrPhotoCurationConflict),
		errors.Is(err, resource.ErrReviewResourceConflict):
		writeError(c, errx.NewConflict("回顾照片已更新，请刷新后重试"))
	default:
		writeAdminCatalogError(c, err)
	}
}
