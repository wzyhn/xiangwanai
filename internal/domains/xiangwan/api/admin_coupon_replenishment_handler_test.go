package xiangwanapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type couponReplenishmentStub struct {
	command couponpostgres.ManualReplenishmentCommand
}

func (stub *couponReplenishmentStub) Replenish(
	_ context.Context, command couponpostgres.ManualReplenishmentCommand,
) (couponpostgres.GrantResult, error) {
	stub.command = command
	items := make([]coupon.Coupon, coupon.GrantQuantity)
	for index := range items {
		items[index] = coupon.Coupon{
			ID: uuid.New(), GrantedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		}
	}
	return couponpostgres.GrantResult{
		Grant: coupon.Grant{BusinessKey: command.BusinessKey, Coupons: items},
	}, nil
}

func serveCouponReplenishment(tenantID uuid.UUID, stub *couponReplenishmentStub, request *http.Request) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set(adminPrincipalContextKey, xiangwanAdminPrincipalForTest()) })
	handler := NewAdminCatalogHandler(photoCurationCatalogStub{})
	if stub != nil {
		handler.SetCouponReplenisher(tenantID, stub)
	}
	handler.RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
	result := httptest.NewRecorder()
	engine.ServeHTTP(result, request)
	return result
}

func TestCouponReplenishmentRequiresInjectedWriter(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/coupon-grants", strings.NewReader("{}"))
	if result := serveCouponReplenishment(uuid.New(), nil, request); result.Code != http.StatusNotFound {
		t.Fatalf("unconfigured replenishment route = %d", result.Code)
	}
}

func TestCouponReplenishmentDerivesOwnerAndActorServerSide(t *testing.T) {
	tenantID, sourceID, operationID := uuid.New(), uuid.New(), uuid.New()
	stub := &couponReplenishmentStub{}
	body, _ := json.Marshal(ReplenishCouponRequest{
		SourceCouponID: sourceID.String(), OperationID: operationID.String(),
		Reason: "case reviewed", Context: "support-case-001",
	})
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/coupon-grants", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	result := serveCouponReplenishment(tenantID, stub, request)
	if result.Code != http.StatusOK || stub.command.TenantID != tenantID ||
		stub.command.SourceCouponID != sourceID || stub.command.ActorID == uuid.Nil ||
		stub.command.IdentityLinkID == uuid.Nil ||
		stub.command.BusinessKey != "manual:"+sourceID.String()+":"+operationID.String() ||
		!strings.Contains(result.Body.String(), `"coupon_ids"`) ||
		strings.Contains(result.Body.String(), "principal_id") {
		t.Fatalf("replenishment = %d command=%+v body=%s", result.Code, stub.command, result.Body.String())
	}
}

func TestCouponReplenishmentRejectsCallerChosenPrincipal(t *testing.T) {
	tenantID, sourceID := uuid.New(), uuid.New()
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/coupon-grants",
		strings.NewReader(`{"source_coupon_id":"`+sourceID.String()+`","operation_id":"`+uuid.NewString()+`","reason":"case","context":"support-1","principal_id":"`+uuid.NewString()+`"}`))
	request.Header.Set("Content-Type", "application/json")
	stub := &couponReplenishmentStub{}
	result := serveCouponReplenishment(tenantID, stub, request)
	if result.Code != http.StatusBadRequest || stub.command.ActorID != uuid.Nil {
		t.Fatalf("caller-selected principal accepted: %d %+v", result.Code, stub.command)
	}
}
