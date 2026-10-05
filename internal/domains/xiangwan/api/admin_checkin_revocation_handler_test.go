package xiangwanapi

import (
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	checkinpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin/postgres"
	"context"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type stubCheckinAdministration struct {
	read   func(context.Context, xiangwanadmin.Principal, uuid.UUID, string) (checkin.Checkin, error)
	revoke func(context.Context, xiangwanadmin.RevokeCheckinCommand) (xiangwanadmin.CheckinRevocationResult, error)
}

func (s stubCheckinAdministration) GetRegistrationCheckin(ctx context.Context, p xiangwanadmin.Principal, id uuid.UUID, request string) (checkin.Checkin, error) {
	return s.read(ctx, p, id, request)
}
func (s stubCheckinAdministration) RevokeRegistrationCheckin(ctx context.Context, c xiangwanadmin.RevokeCheckinCommand) (xiangwanadmin.CheckinRevocationResult, error) {
	return s.revoke(ctx, c)
}

func TestAdminCheckinRevocationTrustsPrincipalAndOmitsPrivateIdentities(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	registrationID, checkinID, operationID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	serve := func(method, path, body string, authenticate bool) *httptest.ResponseRecorder {
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		if authenticate {
			engine.Use(func(c *gin.Context) { c.Set(adminPrincipalContextKey, principal) })
		}
		handler := NewAdminCatalogHandler(stubAdminCatalog{})
		handler.checkinAdministration = stubCheckinAdministration{
			read: func(_ context.Context, p xiangwanadmin.Principal, id uuid.UUID, request string) (checkin.Checkin, error) {
				if p.PrincipalID != principal.PrincipalID || id != registrationID || request == "" {
					t.Fatal("untrusted read identity")
				}
				return checkin.Checkin{ID: checkinID, RegistrationID: registrationID, PrincipalID: uuid.New(), CheckedInBy: principal.PrincipalID, Version: 1, CheckinStatus: checkin.StatusCheckedIn, CheckedInAt: now}, nil
			},
			revoke: func(_ context.Context, c xiangwanadmin.RevokeCheckinCommand) (xiangwanadmin.CheckinRevocationResult, error) {
				if c.ActorID != principal.PrincipalID || c.IdentityLinkID != principal.IdentityLinkID || c.RegistrationID != registrationID || c.CheckinID != checkinID || c.OperationID != operationID || c.ExpectedVersion != 1 || c.Reason != "误签" {
					t.Fatalf("command %+v", c)
				}
				return xiangwanadmin.CheckinRevocationResult{}, checkinpostgres.ErrCheckinRevocationVersionConflict
			},
		}
		handler.RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
		r := adminCancellationRequest(method, path, body, operationID.String())
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, r)
		return recorder
	}
	path := "/api/v1/xiangwan/admin/registrations/" + registrationID.String()
	read := serve("GET", path+"/checkin", "", true)
	if read.Code != 200 || read.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("read %d %s", read.Code, read.Body.String())
	}
	if strings.Contains(read.Body.String(), "principal_id") || strings.Contains(read.Body.String(), "checked_in_by") {
		t.Fatal("private identities leaked")
	}
	body := `{"checkin_id":"` + checkinID.String() + `","expected_version":1,"reason":"误签"}`
	if r := serve("POST", path+"/checkin-revocations", body, true); r.Code != 409 {
		t.Fatalf("conflict %d %s", r.Code, r.Body.String())
	}
	if r := serve("POST", path+"/checkin-revocations", body, false); r.Code != 401 {
		t.Fatal(r.Code)
	}
	if r := serve("POST", path+"/checkin-revocations", strings.TrimSuffix(body, "}")+`,"actor_id":"foreign"}`, true); r.Code != 400 {
		t.Fatal(r.Code)
	}
}
