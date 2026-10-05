package xiangwanapi

import (
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"context"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type bindingAcceptorProbe struct {
	owner, operation, profile uuid.UUID
	calls                     int
	code                      string
}

func (p *bindingAcceptorProbe) Preview(_ context.Context, owner uuid.UUID, code string) (peoplepostgres.BindingInvitationPreview, error) {
	p.owner = owner
	p.code = code
	p.calls++
	return peoplepostgres.BindingInvitationPreview{PeopleProfileID: p.profile, DisplayName: "人物", ProfileVersion: 2, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (p *bindingAcceptorProbe) Accept(_ context.Context, owner, operation uuid.UUID, code string, version int64, consent bool) (peoplepostgres.BindingInvitationConfirmation, error) {
	p.owner = owner
	p.operation = operation
	p.code = code
	p.calls++
	if version != 2 || !consent {
		return peoplepostgres.BindingInvitationConfirmation{}, peoplepostgres.ErrBindingInvitationInvalid
	}
	return peoplepostgres.BindingInvitationConfirmation{PeopleProfileID: p.profile, Status: "active"}, nil
}
func TestPeopleBindingConsumerBoundaryUsesTrustedIdentityAndRejectsInjection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owner, operation := uuid.New(), uuid.New()
	probe := &bindingAcceptorProbe{profile: uuid.New()}
	serve := func(path, body string, authenticated bool, key string) *httptest.ResponseRecorder {
		engine := gin.New()
		handler := NewPeopleBindingHandler(probe, func(c *gin.Context) (uuid.UUID, error) {
			if !authenticated {
				return uuid.Nil, errx.NewUnauthorized("authentication required")
			}
			return owner, nil
		}, func(c *gin.Context) { c.Next() })
		handler.RegisterRoutes(engine.Group("/api/v1/xiangwan"))
		request := httptest.NewRequest("POST", "/api/v1/xiangwan"+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		return recorder
	}
	code := uuid.NewString() + "." + strings.Repeat("a", 43)
	body := `{"code":"` + code + `","expected_version":2,"consent":true}`
	result := serve("/me/people-bindings", body, true, operation.String())
	if result.Code != 200 || probe.owner != owner || probe.operation != operation || probe.code != code || result.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("trusted confirmation %d %s", result.Code, result.Body.String())
	}
	if strings.Contains(result.Body.String(), code) || strings.Contains(result.Body.String(), owner.String()) || strings.Contains(result.Body.String(), "principal_id") {
		t.Fatal("private evidence leaked")
	}
	for _, input := range []struct {
		path, body, key string
		auth            bool
		status          int
	}{
		{"/me/people-bindings", body, operation.String(), false, 401},
		{"/me/people-bindings", body, "", true, 400},
		{"/me/people-bindings", strings.TrimSuffix(body, "}") + `,"principal_id":"foreign"}`, operation.String(), true, 400},
		{"/me/people-bindings?owner=foreign", body, operation.String(), true, 400},
		{"/me/people-binding-preview", `{"code":"` + code + `"}`, operation.String(), true, 400},
	} {
		before := probe.calls
		r := serve(input.path, input.body, input.auth, input.key)
		if r.Code != input.status || probe.calls != before {
			t.Fatalf("invalid boundary %s %d", input.path, r.Code)
		}
	}
	preview := serve("/me/people-binding-preview", `{"code":"`+code+`"}`, true, "")
	if preview.Code != 200 || preview.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(preview.Code)
	}
}
