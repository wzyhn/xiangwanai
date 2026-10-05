package xiangwanapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type refundActionOperatorStub struct {
	called  bool
	command xiangwanadmin.RefundActionCommand
}

type refundActionCatalogStub struct{ xiangwanadmin.Catalog }

func (stub *refundActionOperatorStub) TransitionRefund(
	_ context.Context, command xiangwanadmin.RefundActionCommand,
) (xiangwanadmin.RefundActionResult, error) {
	stub.called = true
	stub.command = command
	return xiangwanadmin.RefundActionResult{
		CaseID: command.CaseID, Status: refund.StatusRefunded,
		Version: 3, SuccessfulRefundCents: command.SuccessfulRefundCents,
		EventSequence: 2,
	}, nil
}

func serveRefundAction(
	principal *xiangwanadmin.Principal,
	operator xiangwanadmin.RefundOperator,
	request *http.Request,
) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if principal != nil {
		engine.Use(func(c *gin.Context) { c.Set(adminPrincipalContextKey, *principal) })
	}
	handler := NewAdminCatalogHandler(refundActionCatalogStub{})
	handler.SetRefundOperator(operator)
	handler.RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func TestRefundActionPassesExactEvidenceAndReturnsSafeReceipt(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	operator := &refundActionOperatorStub{}
	caseID, operationID := apiUUID(221), uuid.New()
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/refund-cases/"+caseID.String()+"/actions",
		strings.NewReader(`{"action":"complete","expected_version":2,`+
			`"successful_refund_cents":"9007199254740993",`+
			`"external_refund_id":"wx-refund-1",`+
			`"evidence_reference":"merchant-record-1"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID.String())
	recorder := serveRefundAction(&principal, operator, request)
	if recorder.Code != http.StatusOK || !operator.called ||
		operator.command.CaseID != caseID || operator.command.OperationID != operationID ||
		operator.command.ActorID != principal.PrincipalID ||
		operator.command.IdentityLinkID != principal.IdentityLinkID ||
		operator.command.ExpectedVersion != 2 ||
		operator.command.SuccessfulRefundCents != 9007199254740993 ||
		operator.command.ExternalRefundID != "wx-refund-1" ||
		operator.command.EvidenceReference != "merchant-record-1" {
		t.Fatalf("action status=%d command=%+v body=%s", recorder.Code, operator.command, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"successful_refund_cents":"9007199254740993"`) ||
		strings.Contains(body, "wx-refund-1") || strings.Contains(body, "merchant-record-1") ||
		recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("unsafe action response: %s", body)
	}
}

func TestRefundActionRejectsMalformedAmountBeforeOperator(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	operator := &refundActionOperatorStub{}
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/refund-cases/"+apiUUID(222).String()+"/actions",
		strings.NewReader(`{"action":"complete","expected_version":1,`+
			`"successful_refund_cents":"9.99"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	recorder := serveRefundAction(&principal, operator, request)
	if recorder.Code != http.StatusBadRequest || operator.called {
		t.Fatalf("malformed amount status=%d called=%t", recorder.Code, operator.called)
	}
}
