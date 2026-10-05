package xiangwanapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

type reviewStatusReadCatalog struct {
	xiangwanadmin.Catalog
	read func(context.Context, xiangwanadmin.Principal, uuid.UUID) (xiangwanadmin.InstanceReviewStatus, error)
}

func (catalog reviewStatusReadCatalog) GetInstanceReviewStatus(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	instanceID uuid.UUID,
) (xiangwanadmin.InstanceReviewStatus, error) {
	return catalog.read(ctx, principal, instanceID)
}

func TestGetInstanceReviewStatusRequiresAdminPrincipalAndExactQuery(t *testing.T) {
	t.Parallel()

	instanceID := apiUUID(190)
	principal := xiangwanAdminPrincipalForTest()
	called := false
	catalog := reviewStatusReadCatalog{read: func(context.Context, xiangwanadmin.Principal, uuid.UUID) (xiangwanadmin.InstanceReviewStatus, error) {
		called = true
		return xiangwanadmin.InstanceReviewStatus{}, nil
	}}
	withoutPrincipal := serveAdminCatalog(
		catalog,
		nil,
		httpRequest(http.MethodGet, "/api/v1/xiangwan/admin/instances/"+instanceID.String()+"/review-status"),
	)
	if withoutPrincipal.Code != http.StatusUnauthorized || called {
		t.Fatalf("without principal status=%d called=%t", withoutPrincipal.Code, called)
	}
	withUnexpectedQuery := serveAdminCatalog(
		catalog,
		&principal,
		httpRequest(http.MethodGet, "/api/v1/xiangwan/admin/instances/"+instanceID.String()+"/review-status?preview=1"),
	)
	if withUnexpectedQuery.Code != http.StatusBadRequest || called {
		t.Fatalf("unexpected query status=%d called=%t", withUnexpectedQuery.Code, called)
	}
}

func TestGetInstanceReviewStatusProjectsReadOnlyPublicationFacts(t *testing.T) {
	t.Parallel()

	instanceID := apiUUID(191)
	sessionID := apiUUID(192)
	principal := xiangwanAdminPrincipalForTest()
	publishedAt := time.Date(2026, time.September, 27, 8, 30, 0, 0, time.UTC)
	called := false
	catalog := reviewStatusReadCatalog{read: func(_ context.Context, actor xiangwanadmin.Principal, gotID uuid.UUID) (xiangwanadmin.InstanceReviewStatus, error) {
		called = true
		if actor.PrincipalID != principal.PrincipalID || actor.IdentityLinkID != principal.IdentityLinkID || gotID != instanceID {
			t.Fatalf("review status command actor/target = %+v %s", actor, gotID)
		}
		return xiangwanadmin.InstanceReviewStatus{
			InstanceID:                  instanceID,
			InstanceStatus:              activity.InstanceStatusCompleted,
			PublicReviewEligible:        true,
			PublicReviewAvailable:       true,
			InstanceReviewDocumentCount: 2,
			PublicSessionResourceCount:  3,
			LatestPublishedAt:           &publishedAt,
			Sessions: []xiangwanadmin.SessionReviewStatus{{
				SessionID: sessionID, Status: activity.SessionStatusArchived,
				PublicResourceCount: 3, PublicReviewAvailable: true,
				LatestPublishedAt: &publishedAt,
			}},
		}, nil
	}}
	recorder := serveAdminCatalog(
		catalog,
		&principal,
		httpRequest(http.MethodGet, "/api/v1/xiangwan/admin/instances/"+instanceID.String()+"/review-status"),
	)
	if recorder.Code != http.StatusOK || !called {
		t.Fatalf("status=%d called=%t body=%s", recorder.Code, called, recorder.Body.String())
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			InstanceID                  string  `json:"instance_id"`
			PublicReviewAvailable       bool    `json:"public_review_available"`
			InstanceReviewDocumentCount int     `json:"instance_review_document_count"`
			PublicSessionResourceCount  int     `json:"public_session_resource_count"`
			LatestPublishedAt           *string `json:"latest_published_at"`
			Sessions                    []struct {
				SessionID           string `json:"session_id"`
				PublicResourceCount int    `json:"public_resource_count"`
			} `json:"sessions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.InstanceID != instanceID.String() ||
		!envelope.Data.PublicReviewAvailable || envelope.Data.InstanceReviewDocumentCount != 2 ||
		envelope.Data.PublicSessionResourceCount != 3 || envelope.Data.LatestPublishedAt == nil ||
		len(envelope.Data.Sessions) != 1 || envelope.Data.Sessions[0].SessionID != sessionID.String() ||
		envelope.Data.Sessions[0].PublicResourceCount != 3 {
		t.Fatalf("response data=%+v", envelope.Data)
	}
	if body := recorder.Body.String(); containsAny(body, []string{"external_url", "storage_key", "https://"}) {
		t.Fatalf("read-only status leaked resource body or URL: %s", body)
	}
}

func httpRequest(method, target string) *http.Request {
	return httptest.NewRequest(method, target, nil)
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
