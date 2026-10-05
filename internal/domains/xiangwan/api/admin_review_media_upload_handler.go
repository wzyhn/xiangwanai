package xiangwanapi

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	storagestand "github.com/wzyhn/xiangwanai/internal/capabilities/storage/standalonepg"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type IssueAdminReviewMediaUploadRequest struct {
	InstanceID string                        `json:"instance_id"`
	SessionID  string                        `json:"session_id,omitempty"`
	Kind       xiangwanadmin.ReviewMediaKind `json:"kind"`
	MIME       string                        `json:"mime"`
	Size       int64                         `json:"size"`
	SHA256     string                        `json:"sha256"`
}

type ConfirmAdminReviewMediaUploadRequest struct {
	FileID string `json:"file_id"`
	SHA256 string `json:"sha256"`
}

type AdminReviewMediaUploadResponse struct {
	FileID      string                        `json:"file_id"`
	InstanceID  string                        `json:"instance_id"`
	SessionID   string                        `json:"session_id,omitempty"`
	Kind        xiangwanadmin.ReviewMediaKind `json:"kind"`
	MIME        string                        `json:"mime"`
	Size        int64                         `json:"size"`
	SHA256      string                        `json:"sha256"`
	UploadURL   string                        `json:"upload_url"`
	PreviewURL  string                        `json:"preview_url,omitempty"`
	ExpiresAt   string                        `json:"expires_at"`
	ConfirmedAt string                        `json:"confirmed_at,omitempty"`
}

// IssueReviewMediaUpload godoc
// @Summary Issue a target-bound, short-lived administrator review-media upload intent
// @Description Pending customer content-safety policy and production composition: this handler is not registered by the production runtime. The file ID is the UUIDv4 operation key. No arbitrary bucket/key or public URL is accepted.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body IssueAdminReviewMediaUploadRequest true "Target-bound review media upload"
// @Success 200 {object} AdminReviewMediaUploadResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
func (handler *AdminCatalogHandler) IssueReviewMediaUpload(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if handler.mediaUploads == nil {
		writeError(c, fmt.Errorf("review media uploads unavailable"))
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	var payload IssueAdminReviewMediaUploadRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	instanceID, err := parseCanonicalUUID(payload.InstanceID)
	if err != nil {
		writeReviewMediaUploadError(c, xiangwanadmin.ErrInvalidMediaUpload)
		return
	}
	var sessionID *uuid.UUID
	if payload.SessionID != "" {
		parsed, parseErr := parseCanonicalUUID(payload.SessionID)
		if parseErr != nil {
			writeReviewMediaUploadError(c, xiangwanadmin.ErrInvalidMediaUpload)
			return
		}
		sessionID = &parsed
	}
	intent, err := handler.mediaUploads.IssueReviewMediaUpload(c.Request.Context(), principal,
		xiangwanadmin.IssueReviewMediaUploadCommand{
			OperationID: operationID, InstanceID: instanceID, SessionID: sessionID,
			Kind: payload.Kind, MIME: payload.MIME,
			Size: payload.Size, SHA256: payload.SHA256,
		})
	if err != nil {
		writeReviewMediaUploadError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	response.OK(c, projectReviewMediaUpload(intent))
}

// StageReviewMediaBytes godoc
// @Summary Stage exact review-media bytes for a target-bound upload intent
// @Description Pending customer content-safety policy and production composition: this handler is not registered by the production runtime. Requires exact size, SHA-256 and MIME magic; staging never publishes bytes.
// @Tags xiangwan-admin
// @Accept application/octet-stream
// @Produce json
// @Param file_id path string true "Issued File ID"
// @Success 200 {object} AdminReviewMediaUploadResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
func (handler *AdminCatalogHandler) StageReviewMediaBytes(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok {
		return
	}
	if handler.mediaUploads == nil {
		writeError(c, fmt.Errorf("review media uploads unavailable"))
		return
	}
	if !validAdminQuery(c) || c.ContentType() != "application/octet-stream" ||
		c.GetHeader("Content-Encoding") != "" || c.Request.ContentLength == 0 ||
		c.Request.ContentLength > storagestand.MaxReviewObjectBytes {
		writeReviewMediaUploadError(c, xiangwanadmin.ErrInvalidMediaUpload)
		return
	}
	fileID, err := parseCanonicalUUID(c.Param("file_id"))
	if err != nil {
		writeReviewMediaUploadError(c, xiangwanadmin.ErrInvalidMediaUpload)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body,
		storagestand.MaxReviewObjectBytes+1)
	value, err := handler.mediaUploads.StageReviewMediaBytes(c.Request.Context(), principal,
		fileID, c.Request.Body)
	if err != nil {
		writeReviewMediaUploadError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	response.OK(c, projectReviewMediaUpload(value))
}

// ConfirmReviewMediaUpload godoc
// @Summary Confirm provider-inspected review-media bytes
// @Description Pending customer content-safety policy and production composition: this handler is not registered by the production runtime. Confirmation remains private and requires a separate reviewed resource write and permanent File pin.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param request body ConfirmAdminReviewMediaUploadRequest true "Exact File ID and SHA-256"
// @Success 200 {object} AdminReviewMediaUploadResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
func (handler *AdminCatalogHandler) ConfirmReviewMediaUpload(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if handler.mediaUploads == nil {
		writeError(c, fmt.Errorf("review media uploads unavailable"))
		return
	}
	var payload ConfirmAdminReviewMediaUploadRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	fileID, err := parseCanonicalUUID(payload.FileID)
	if err != nil {
		writeReviewMediaUploadError(c, xiangwanadmin.ErrInvalidMediaUpload)
		return
	}
	value, err := handler.mediaUploads.ConfirmReviewMediaUpload(c.Request.Context(), principal,
		fileID, payload.SHA256)
	if err != nil {
		writeReviewMediaUploadError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	response.OK(c, projectReviewMediaUpload(value))
}

// PreviewReviewMediaBytes godoc
// @Summary Privately inspect exact confirmed review-media bytes
// @Description Dormant until the production media safety and binding chain is complete. Requires the current uploader's exact administrator identity and live target; no public URL or provider key is returned.
// @Tags xiangwan-admin
// @Produce application/octet-stream
// @Param file_id path string true "Issued File ID"
// @Success 200 {file} file
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
func (handler *AdminCatalogHandler) PreviewReviewMediaBytes(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c) {
		return
	}
	if handler.mediaUploads == nil {
		writeError(c, fmt.Errorf("review media uploads unavailable"))
		return
	}
	fileID, err := parseCanonicalUUID(c.Param("file_id"))
	if err != nil {
		writeReviewMediaUploadError(c, xiangwanadmin.ErrInvalidMediaUpload)
		return
	}
	preview, err := handler.mediaUploads.OpenReviewMediaPreview(
		c.Request.Context(), principal, fileID,
	)
	if err != nil {
		writeReviewMediaUploadError(c, err)
		return
	}
	extension := map[string]string{
		"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp",
		"video/mp4": "mp4", "audio/mpeg": "mp3", "application/pdf": "pdf",
	}[preview.MIME]
	if extension == "" || preview.Reader == nil || preview.Size <= 0 {
		if preview.Reader != nil {
			_ = preview.Reader.Close()
		}
		writeReviewMediaUploadError(c, xiangwanadmin.ErrMediaUploadConflict)
		return
	}
	defer func() { _ = preview.Reader.Close() }()
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "sandbox")
	c.Header("Content-Disposition", fmt.Sprintf(
		"attachment; filename=review-%s.%s", fileID, extension,
	))
	c.Header("Content-Type", preview.MIME)
	http.ServeContent(c.Writer, c.Request, "", time.Time{}, preview.Reader)
}

func projectReviewMediaUpload(value xiangwanadmin.ReviewMediaUpload) AdminReviewMediaUploadResponse {
	result := AdminReviewMediaUploadResponse{
		FileID: value.FileID.String(), InstanceID: value.InstanceID.String(),
		Kind: value.Kind, MIME: value.MIME, Size: value.Size, SHA256: value.SHA256,
		UploadURL: "/api/v1/xiangwan/admin/media-upload-intents/" +
			value.FileID.String() + "/bytes",
		ExpiresAt: value.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	if value.SessionID != nil {
		result.SessionID = value.SessionID.String()
	}
	if value.ConfirmedAt != nil {
		result.ConfirmedAt = value.ConfirmedAt.UTC().Format(time.RFC3339Nano)
		result.PreviewURL = "/api/v1/xiangwan/admin/media-upload-intents/" +
			value.FileID.String() + "/preview"
	}
	return result
}

func writeReviewMediaUploadError(c *gin.Context, err error) {
	var maxBytes *http.MaxBytesError
	switch {
	case errors.As(err, &maxBytes):
		writeError(c, errx.New(errx.CodeFileTooLarge, "活动资料超过上传大小限制"))
	case errors.Is(err, xiangwanadmin.ErrInvalidMediaUpload):
		writeError(c, errx.NewBadRequest("活动资料格式、大小或内容无效"))
	case errors.Is(err, xiangwanadmin.ErrMediaUploadExpired):
		writeError(c, errx.NewConflict("活动资料上传已过期，请重新开始"))
	case errors.Is(err, xiangwanadmin.ErrMediaUploadConflict):
		writeError(c, errx.NewConflict("活动资料与上传意图不一致，请刷新后重试"))
	default:
		writeAdminCatalogError(c, err)
	}
}
