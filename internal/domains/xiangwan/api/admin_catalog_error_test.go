package xiangwanapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/gin-gonic/gin"
)

func TestWriteAdminCatalogErrorMapsUnavailableQuickTagToStableConflict(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)

	writeAdminCatalogError(context, xiangwanadmin.ErrQuickTagNotPublished)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != int(errx.CodeXiangwanAdminQuickTagUnavailable) {
		t.Fatalf("code = %d, want %d", body.Code, errx.CodeXiangwanAdminQuickTagUnavailable)
	}
	if body.Message != "活动标签配置已变化，请刷新后重试" {
		t.Fatalf("message = %q, want stable quick-tag conflict message", body.Message)
	}
}

func TestWriteAdminCatalogErrorMapsMissingSessionToStableNotFound(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)

	writeAdminCatalogError(context, activitypostgres.ErrSessionNotFound)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	var body struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != int(errx.CodeXiangwanAdminTargetNotFound) {
		t.Fatalf("code = %d, want %d", body.Code, errx.CodeXiangwanAdminTargetNotFound)
	}
}
