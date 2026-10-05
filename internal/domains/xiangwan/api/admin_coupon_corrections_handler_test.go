package xiangwanapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type couponCorrectionReaderStub struct {
	filter xiangwanadmin.CouponCorrectionFilter
	actor  xiangwanadmin.Principal
}

func (stub *couponCorrectionReaderStub) ListCouponCorrections(
	_ context.Context, actor xiangwanadmin.Principal, filter xiangwanadmin.CouponCorrectionFilter,
) (xiangwanadmin.CouponCorrectionPage, error) {
	stub.actor, stub.filter = actor, filter
	asOf := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	orderID := apiUUID(252)
	return xiangwanadmin.CouponCorrectionPage{
		Items: []xiangwanadmin.CouponCorrectionItem{{
			EntryID: apiUUID(253), CouponID: apiUUID(254),
			SourceCheckinEventID: apiUUID(255), RelatedEntryType: "redeemed",
			OrderID: &orderID, RecordedAt: asOf.Add(-time.Hour),
		}},
		Page: 1, PageSize: 50, Total: 1, AsOf: asOf,
	}, nil
}

func serveCouponCorrections(stub *couponCorrectionReaderStub, request *http.Request) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	principal := xiangwanAdminPrincipalForTest()
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set(adminPrincipalContextKey, principal) })
	handler := NewAdminCatalogHandler(photoCurationCatalogStub{})
	handler.SetCouponCorrectionReader(stub)
	handler.RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func TestAdminCouponCorrectionListRejectsUnstablePagination(t *testing.T) {
	t.Parallel()
	stub := &couponCorrectionReaderStub{}
	for _, suffix := range []string{"?page=2", "?as_of=invalid", "?unexpected=1"} {
		request := httptest.NewRequest(http.MethodGet,
			"/api/v1/xiangwan/admin/coupon-corrections"+suffix, nil)
		result := serveCouponCorrections(stub, request)
		if result.Code != http.StatusBadRequest || stub.actor.PrincipalID != uuid.Nil {
			t.Fatalf("invalid correction query %q = %d, called=%s", suffix, result.Code, stub.actor.PrincipalID)
		}
	}
}

func TestAdminCouponCorrectionListExposesRoutingMetadataOnly(t *testing.T) {
	t.Parallel()
	stub := &couponCorrectionReaderStub{}
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/xiangwan/admin/coupon-corrections?page=1&page_size=50", nil)
	result := serveCouponCorrections(stub, request)
	if result.Code != http.StatusOK || result.Header().Get("Cache-Control") != "private, no-store" ||
		stub.actor.PrincipalID == uuid.Nil || stub.filter.Page != 1 || stub.filter.PageSize != 50 {
		t.Fatalf("correction list = %d, actor=%s filter=%+v", result.Code, stub.actor.PrincipalID, stub.filter)
	}
	body := result.Body.String()
	if !strings.Contains(body, `"related_entry_type":"redeemed"`) ||
		!strings.Contains(body, apiUUID(252).String()) ||
		strings.Contains(body, "principal_id") || strings.Contains(body, "reason") ||
		strings.Contains(body, "grant_business_key") {
		t.Fatalf("unsafe correction projection: %s", body)
	}
}

func TestAdminCouponCorrectionListForwardsSnapshotOnLaterPages(t *testing.T) {
	t.Parallel()
	stub := &couponCorrectionReaderStub{}
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/xiangwan/admin/coupon-corrections?page=2&page_size=25&as_of=2026-10-01T02%3A00%3A00Z", nil)
	result := serveCouponCorrections(stub, request)
	if result.Code != http.StatusOK || stub.filter.Page != 2 || stub.filter.PageSize != 25 ||
		stub.filter.AsOf == nil || !stub.filter.AsOf.Equal(time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)) {
		t.Fatalf("later correction page = %d, filter=%+v", result.Code, stub.filter)
	}
}
