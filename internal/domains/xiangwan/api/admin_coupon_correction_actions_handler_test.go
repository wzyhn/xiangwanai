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

type couponCorrectionActionStub struct {
	called  bool
	command xiangwanadmin.CouponCorrectionActionCommand
}

func (stub *couponCorrectionActionStub) TransitionCouponCorrection(_ context.Context,
	command xiangwanadmin.CouponCorrectionActionCommand) (xiangwanadmin.CouponCorrectionActionResult, error) {
	stub.called, stub.command = true, command
	return xiangwanadmin.CouponCorrectionActionResult{
		CorrectionEntryID: command.CorrectionEntryID, CouponID: apiUUID(240),
		Status: "resolved", Version: 2, AdjustmentCents: command.AdjustmentCents,
		RecordedAt: time.Now().UTC(),
	}, nil
}

func serveCouponCorrectionAction(stub *couponCorrectionActionStub, principal xiangwanadmin.Principal, request *http.Request) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set(adminPrincipalContextKey, principal) })
	handler := NewAdminCatalogHandler(photoCurationCatalogStub{})
	handler.SetCouponCorrectionOperator(stub)
	handler.RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func TestCouponCorrectionActionKeepsExactAmountAndPrivateEvidence(t *testing.T) {
	t.Parallel()
	stub := &couponCorrectionActionStub{}
	entryID, operationID := apiUUID(241), uuid.New()
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/coupon-corrections/"+entryID.String()+"/actions",
		strings.NewReader(`{"action":"resolve","expected_version":1,"evidence_kind":"financial",`+
			`"evidence_reference":"official-ledger-001","adjustment_cents":"9007199254740993",`+
			`"operator_note":"verified official record"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID.String())
	principal := xiangwanAdminPrincipalForTest()
	result := serveCouponCorrectionAction(stub, principal, request)
	if result.Code != http.StatusOK || !stub.called || stub.command.ActorID != principal.PrincipalID ||
		stub.command.IdentityLinkID != principal.IdentityLinkID || stub.command.OperationID != operationID ||
		stub.command.CorrectionEntryID != entryID || stub.command.RequestID == "" ||
		stub.command.AdjustmentCents != 9007199254740993 ||
		stub.command.EvidenceReference != "official-ledger-001" ||
		!strings.Contains(result.Body.String(), `"adjustment_cents":"9007199254740993"`) ||
		strings.Contains(result.Body.String(), "official-ledger-001") ||
		strings.Contains(result.Body.String(), "verified official record") ||
		result.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("unsafe or inexact correction action status=%d command=%+v body=%s", result.Code, stub.command, result.Body.String())
	}
}

func TestCouponCorrectionActionRejectsClientIdentityAndMalformedAmount(t *testing.T) {
	t.Parallel()
	for _, payload := range []string{
		`{"action":"resolve","adjustment_cents":"1.25","operator_note":"review"}`,
		`{"action":"start","operator_note":"review","actor_id":"` + apiUUID(242).String() + `"}`,
	} {
		stub := &couponCorrectionActionStub{}
		request := httptest.NewRequest(http.MethodPost,
			"/api/v1/xiangwan/admin/coupon-corrections/"+apiUUID(243).String()+"/actions", strings.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", uuid.NewString())
		if result := serveCouponCorrectionAction(stub, xiangwanAdminPrincipalForTest(), request); result.Code != http.StatusBadRequest || stub.called {
			t.Fatalf("invalid correction body accepted: %d called=%t", result.Code, stub.called)
		}
	}
}
