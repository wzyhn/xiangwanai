package xiangwanapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type policyActivationStub struct {
	command coupon.PolicyActivationCommand
}

func (stub *policyActivationStub) Activate(_ context.Context, command coupon.PolicyActivationCommand) (coupon.PolicyActivationReceipt, error) {
	stub.command = command
	return coupon.PolicyActivationReceipt{
		PolicyVersion: "guest-v1", Enabled: true,
		EffectiveAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		RecordedAt:  time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}, nil
}

func serveCouponPolicyActivation(stub *policyActivationStub, request *http.Request) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set(adminPrincipalContextKey, xiangwanAdminPrincipalForTest()) })
	handler := NewAdminCatalogHandler(photoCurationCatalogStub{})
	if stub != nil {
		handler.SetCouponPolicyActivationWriter(stub)
	}
	handler.RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
	result := httptest.NewRecorder()
	engine.ServeHTTP(result, request)
	return result
}

func TestCouponPolicyRouteIsAbsentWithoutSignedWriter(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/coupon-grant-policies", strings.NewReader("{}"))
	if result := serveCouponPolicyActivation(nil, request); result.Code != http.StatusNotFound {
		t.Fatalf("unconfigured policy route = %d", result.Code)
	}
}

func TestCouponPolicyRoutePassesOnlySignedBytesAndActor(t *testing.T) {
	stub := &policyActivationStub{}
	payload, signature := []byte(`{"policy_version":"guest-v1"}`), bytes.Repeat([]byte{7}, 64)
	body, _ := json.Marshal(ActivateCouponGrantPolicyRequest{
		PayloadBase64:   base64.StdEncoding.EncodeToString(payload),
		SignatureBase64: base64.StdEncoding.EncodeToString(signature),
	})
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/coupon-grant-policies", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	result := serveCouponPolicyActivation(stub, request)
	if result.Code != http.StatusOK ||
		stub.command.ActorID == uuid.Nil ||
		stub.command.IdentityLinkID == uuid.Nil ||
		!bytes.Equal(stub.command.Payload, payload) ||
		!bytes.Equal(stub.command.Signature, signature) ||
		strings.Contains(result.Body.String(), "payload_base64") {
		t.Fatalf("signed policy request = %d command=%+v response=%s", result.Code, stub.command, result.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/coupon-grant-policies", strings.NewReader(`{"payload_base64":"*","signature_base64":"*"}`))
	invalid.Header.Set("Content-Type", "application/json")
	if bad := serveCouponPolicyActivation(stub, invalid); bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid signed policy accepted: %d", bad.Code)
	}
}
