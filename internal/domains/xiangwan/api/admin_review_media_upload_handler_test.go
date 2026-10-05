package xiangwanapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type reviewMediaUploadStub struct {
	issued    xiangwanadmin.IssueReviewMediaUploadCommand
	staged    []byte
	confirm   string
	fileID    uuid.UUID
	previewed bool
}

type testReviewMediaReadSeekCloser struct{ *bytes.Reader }

func (*testReviewMediaReadSeekCloser) Close() error { return nil }

func mediaUploadFixture(fileID, instanceID uuid.UUID) xiangwanadmin.ReviewMediaUpload {
	return xiangwanadmin.ReviewMediaUpload{
		FileID: fileID, InstanceID: instanceID, Kind: xiangwanadmin.ReviewMediaPhoto,
		MIME: "image/png", Size: 8, SHA256: strings.Repeat("a", 64),
		ExpiresAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}
}

func (stub *reviewMediaUploadStub) IssueReviewMediaUpload(
	_ context.Context, _ xiangwanadmin.Principal, command xiangwanadmin.IssueReviewMediaUploadCommand,
) (xiangwanadmin.ReviewMediaUpload, error) {
	stub.issued = command
	return mediaUploadFixture(command.OperationID, command.InstanceID), nil
}

func (stub *reviewMediaUploadStub) StageReviewMediaBytes(
	_ context.Context, _ xiangwanadmin.Principal, fileID uuid.UUID, body io.Reader,
) (xiangwanadmin.ReviewMediaUpload, error) {
	stub.fileID = fileID
	stub.staged, _ = io.ReadAll(body)
	return mediaUploadFixture(fileID, apiUUID(240)), nil
}

func (stub *reviewMediaUploadStub) ConfirmReviewMediaUpload(
	_ context.Context, _ xiangwanadmin.Principal, fileID uuid.UUID, checksum string,
) (xiangwanadmin.ReviewMediaUpload, error) {
	stub.fileID, stub.confirm = fileID, checksum
	result := mediaUploadFixture(fileID, apiUUID(240))
	confirmed := time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)
	result.ConfirmedAt = &confirmed
	return result, nil
}

func (stub *reviewMediaUploadStub) OpenReviewMediaPreview(
	_ context.Context, _ xiangwanadmin.Principal, fileID uuid.UUID,
) (xiangwanadmin.ReviewMediaPreview, error) {
	stub.fileID, stub.previewed = fileID, true
	return xiangwanadmin.ReviewMediaPreview{
		Reader: &testReviewMediaReadSeekCloser{bytes.NewReader([]byte("pngbytes"))},
		MIME:   "image/png", Size: 8,
	}, nil
}

func serveReviewMediaUpload(stub *reviewMediaUploadStub, request *http.Request) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set(adminPrincipalContextKey, xiangwanAdminPrincipalForTest()) })
	handler := NewAdminCatalogHandler(photoCurationCatalogStub{})
	handler.SetReviewMediaUploadService(stub)
	handler.RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
	result := httptest.NewRecorder()
	engine.ServeHTTP(result, request)
	return result
}

func TestAdminReviewMediaIntentBindsOperationAndTargetWithoutProviderKey(t *testing.T) {
	t.Parallel()
	stub := &reviewMediaUploadStub{}
	operationID, instanceID := uuid.New(), uuid.New()
	body, _ := json.Marshal(IssueAdminReviewMediaUploadRequest{
		InstanceID: instanceID.String(), Kind: xiangwanadmin.ReviewMediaPhoto,
		MIME: "image/png", Size: 8,
		SHA256: strings.Repeat("a", 64),
	})
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/media-upload-intents", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID.String())
	result := serveReviewMediaUpload(stub, request)
	if result.Code != http.StatusOK || result.Header().Get("Cache-Control") != "private, no-store" ||
		stub.issued.OperationID != operationID || stub.issued.InstanceID != instanceID {
		t.Fatalf("intent = %d, command=%+v, body=%s", result.Code, stub.issued, result.Body.String())
	}
	if strings.Contains(result.Body.String(), "provider_object_key") ||
		strings.Contains(result.Body.String(), "file_key") ||
		!strings.Contains(result.Body.String(), operationID.String()+"/bytes") {
		t.Fatalf("unsafe or missing intent response: %s", result.Body.String())
	}
}

func TestAdminReviewMediaIntentRejectsUnreviewedClientFilename(t *testing.T) {
	t.Parallel()
	stub := &reviewMediaUploadStub{}
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/media-upload-intents",
		strings.NewReader(`{"instance_id":"`+uuid.NewString()+`","kind":"photo","filename":"unscreened.png","mime":"image/png","size":8,"sha256":"`+strings.Repeat("a", 64)+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	result := serveReviewMediaUpload(stub, request)
	if result.Code != http.StatusBadRequest || stub.issued.OperationID != uuid.Nil {
		t.Fatalf("client filename accepted: status=%d command=%+v", result.Code, stub.issued)
	}
}

func TestAdminReviewMediaBytesRequireExactBinaryRoute(t *testing.T) {
	t.Parallel()
	stub := &reviewMediaUploadStub{}
	fileID := uuid.New()
	url := "/api/v1/xiangwan/admin/media-upload-intents/" + fileID.String() + "/bytes"
	wrong := httptest.NewRequest(http.MethodPut, url, strings.NewReader("bytes"))
	wrong.Header.Set("Content-Type", "image/png")
	if result := serveReviewMediaUpload(stub, wrong); result.Code != http.StatusBadRequest || len(stub.staged) != 0 {
		t.Fatalf("wrong content type = %d, staged=%q", result.Code, stub.staged)
	}
	request := httptest.NewRequest(http.MethodPut, url, strings.NewReader("bytes"))
	request.Header.Set("Content-Type", "application/octet-stream")
	result := serveReviewMediaUpload(stub, request)
	if result.Code != http.StatusOK || stub.fileID != fileID || string(stub.staged) != "bytes" {
		t.Fatalf("binary stage = %d, id=%s, bytes=%q", result.Code, stub.fileID, stub.staged)
	}
}

func TestAdminReviewMediaConfirmationUsesFileAndChecksumBusinessState(t *testing.T) {
	t.Parallel()
	stub := &reviewMediaUploadStub{}
	fileID := uuid.New()
	checksum := strings.Repeat("a", 64)
	body, _ := json.Marshal(ConfirmAdminReviewMediaUploadRequest{FileID: fileID.String(), SHA256: checksum})
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/media-confirmations", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	result := serveReviewMediaUpload(stub, request)
	if result.Code != http.StatusOK || stub.fileID != fileID || stub.confirm != checksum ||
		!strings.Contains(result.Body.String(), `"confirmed_at":"2026-10-01T11:00:00Z"`) {
		t.Fatalf("confirmation = %d, id=%s, checksum=%s, body=%s",
			result.Code, stub.fileID, stub.confirm, result.Body.String())
	}
}

func TestAdminReviewMediaPreviewIsPrivateAttachmentAndSupportsByteRange(t *testing.T) {
	t.Parallel()
	stub := &reviewMediaUploadStub{}
	fileID := uuid.New()
	url := "/api/v1/xiangwan/admin/media-upload-intents/" + fileID.String() + "/preview"
	request := httptest.NewRequest(http.MethodGet, url, nil)
	request.Header.Set("Range", "bytes=0-2")
	result := serveReviewMediaUpload(stub, request)
	if result.Code != http.StatusPartialContent || !stub.previewed || stub.fileID != fileID ||
		result.Body.String() != "png" ||
		result.Header().Get("Cache-Control") != "private, no-store" ||
		result.Header().Get("X-Content-Type-Options") != "nosniff" ||
		result.Header().Get("Content-Security-Policy") != "sandbox" ||
		!strings.HasPrefix(result.Header().Get("Content-Disposition"), "attachment; filename=review-") {
		t.Fatalf("private preview = %d headers=%v body=%q", result.Code, result.Header(), result.Body.String())
	}
	withQuery := httptest.NewRequest(http.MethodGet, url+"?file_key=unsafe", nil)
	if rejected := serveReviewMediaUpload(stub, withQuery); rejected.Code != http.StatusBadRequest {
		t.Fatalf("preview accepted query: %d", rejected.Code)
	}
}
