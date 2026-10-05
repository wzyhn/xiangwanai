package xiangwanapi

import (
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"context"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type hostWithdrawalFake struct {
	fakeHostApplicationSubmissionApplication
	owner, id   uuid.UUID
	version     int64
	withdrawals int
}

func (f *hostWithdrawalFake) Withdraw(_ context.Context, owner, id uuid.UUID, v int64) (peoplepostgres.HostApplicationResult, error) {
	f.owner = owner
	f.id = id
	f.version = v
	f.withdrawals++
	return peoplepostgres.HostApplicationResult{Application: people.HostApplication{ID: id, ApplicationStatus: people.HostApplicationStatusWithdrawn, Version: 2}}, nil
}
func TestHostWithdrawalUsesAuthenticatedOwnerAndRejectsInjectedFacts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := &hostWithdrawalFake{}
	owner, id := uuid.New(), uuid.New()
	e := gin.New()
	NewHostApplicationSubmissionHandler(f, func(*gin.Context) (uuid.UUID, error) { return owner, nil }).RegisterRoutes(e.Group("/api/v1/xiangwan"))
	path := "/api/v1/xiangwan/me/host-applications/" + id.String() + "/withdrawal"
	for _, c := range []struct {
		body, key, query string
		status           int
	}{{`{"expected_version":1}`, "", "", 200}, {`{"expected_version":1,"principal_id":"other"}`, "", "", 400}, {`{"expected_version":1}`, uuid.NewString(), "", 400}, {`{"expected_version":1}`, "", "?principal_id=other", 400}, {`{"expected_version":1} {}`, "", "", 400}} {
		r := httptest.NewRequest(http.MethodPost, path+c.query, strings.NewReader(c.body))
		r.Header.Set("Content-Type", "application/json")
		if c.key != "" {
			r.Header.Set("Idempotency-Key", c.key)
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		if w.Code != c.status {
			t.Fatalf("status %d body %s", w.Code, w.Body.String())
		}
		if c.status == 200 && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("private response cached")
		}
	}
	if f.withdrawals != 1 || f.owner != owner || f.id != id || f.version != 1 {
		t.Fatal("untrusted withdrawal identity")
	}
}

type hostAdminFake struct {
	publish xiangwanadmin.PublishHostRulesCommand
	review  xiangwanadmin.ReviewHostApplicationCommand
	writes  int
}

func (*hostAdminFake) GetHostRules(context.Context, xiangwanadmin.Principal) (xiangwanadmin.HostRuleVersion, error) {
	return xiangwanadmin.HostRuleVersion{}, nil
}
func (*hostAdminFake) ListHostApplications(context.Context, xiangwanadmin.Principal, string, int, int) (xiangwanadmin.HostApplicationPage, error) {
	return xiangwanadmin.HostApplicationPage{}, nil
}
func (*hostAdminFake) GetHostApplication(context.Context, xiangwanadmin.Principal, uuid.UUID, string, string) (xiangwanadmin.HostApplicationDetail, error) {
	return xiangwanadmin.HostApplicationDetail{}, nil
}
func (f *hostAdminFake) PublishHostRules(_ context.Context, c xiangwanadmin.PublishHostRulesCommand) (xiangwanadmin.HostRuleVersion, error) {
	f.publish = c
	f.writes++
	return c.Rules, nil
}
func (f *hostAdminFake) ReviewHostApplication(_ context.Context, c xiangwanadmin.ReviewHostApplicationCommand) (xiangwanadmin.HostApplicationItem, error) {
	f.review = c
	f.writes++
	return xiangwanadmin.HostApplicationItem{ID: c.ApplicationID, Status: c.Decision, Version: 2}, nil
}
func TestAdminHostReviewUsesTrustedActorAndStableOperation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := xiangwanAdminPrincipalForTest()
	f := &hostAdminFake{}
	h := NewAdminCatalogHandler(nil)
	h.hostApplications = f
	e := gin.New()
	e.Use(func(c *gin.Context) { c.Set(adminPrincipalContextKey, p) })
	h.RegisterRoutes(e.Group("/api/v1/xiangwan/admin"))
	id, op := uuid.New(), uuid.New()
	path := "/api/v1/xiangwan/admin/host-applications/" + id.String() + "/reviews"
	w := httptest.NewRecorder()
	e.ServeHTTP(w, adminCancellationRequest("POST", path, `{"expected_version":1,"decision":"approved","comment":"审核通过"}`, op.String()))
	if w.Code != 200 || f.review.ActorID != p.PrincipalID || f.review.IdentityLinkID != p.IdentityLinkID || f.review.OperationID != op || f.review.ApplicationID != id || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("review boundary %d", w.Code)
	}
	w = httptest.NewRecorder()
	e.ServeHTTP(w, adminCancellationRequest("POST", path, `{"expected_version":1,"decision":"approved","comment":"审核通过","reviewer_id":"other"}`, op.String()))
	if w.Code != 400 || f.writes != 1 {
		t.Fatal("untrusted reviewer accepted")
	}
}
func TestHostRuleRiskyTextNeverReachesPolicyWriter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := xiangwanAdminPrincipalForTest()
	f := &hostAdminFake{}
	h := NewAdminCatalogHandler(nil)
	h.hostApplications = f
	h.SetHostRulesModerator(func(context.Context, string) error { return errx.NewBadRequest("内容含违规信息") })
	e := gin.New()
	e.Use(func(c *gin.Context) { c.Set(adminPrincipalContextKey, p) })
	h.RegisterRoutes(e.Group("/api/v1/xiangwan/admin"))
	r := adminCancellationRequest("POST", "/api/v1/xiangwan/admin/host-rules", `{"expected_version":0,"application_cycle":"cycle-v1","policy_version":"host-v1","requirements":"要求","benefits":"支持","enabled":true,"reason":"客户确认"}`, uuid.NewString())
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 400 || f.writes != 0 {
		t.Fatal("risky rule published")
	}
}
