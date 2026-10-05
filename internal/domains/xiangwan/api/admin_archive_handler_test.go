package xiangwanapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

type lifecycleArchiveCatalogStub struct {
	xiangwanadmin.Catalog
	archiveInstance func(context.Context, xiangwanadmin.ArchiveInstanceCommand) (activity.Instance, error)
	archiveSession  func(context.Context, xiangwanadmin.ArchiveSessionCommand) (activity.Session, error)
}

func (stub lifecycleArchiveCatalogStub) ArchiveInstance(
	ctx context.Context,
	command xiangwanadmin.ArchiveInstanceCommand,
) (activity.Instance, error) {
	if stub.archiveInstance == nil {
		panic("unexpected ArchiveInstance call")
	}
	return stub.archiveInstance(ctx, command)
}

func (stub lifecycleArchiveCatalogStub) ArchiveSession(
	ctx context.Context,
	command xiangwanadmin.ArchiveSessionCommand,
) (activity.Session, error) {
	if stub.archiveSession == nil {
		panic("unexpected ArchiveSession call")
	}
	return stub.archiveSession(ctx, command)
}

func TestArchiveInstancePassesExpectedVersionAndProjectsArchivedValue(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	instanceID := apiUUID(230)
	operationID := uuid.New()
	var captured xiangwanadmin.ArchiveInstanceCommand
	catalog := lifecycleArchiveCatalogStub{
		archiveInstance: func(_ context.Context, command xiangwanadmin.ArchiveInstanceCommand) (activity.Instance, error) {
			captured = command
			return activity.Instance{
				ID: instanceID, TenantID: apiUUID(231), SeriesID: apiUUID(232),
				IssueNo: 3, Title: "第 3 期", Status: activity.InstanceStatusArchived,
				PublicationVersion: 2, Version: 9,
			}, nil
		},
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/admin/instances/"+instanceID.String()+"/archives",
		strings.NewReader(`{"expected_instance_version":8}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID.String())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("ArchiveInstance() status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if captured.InstanceID != instanceID || captured.ExpectedInstanceVersion != 8 ||
		captured.OperationID != operationID || captured.ActorID != principal.PrincipalID ||
		captured.IdentityLinkID != principal.IdentityLinkID || captured.RequestID == "" {
		t.Fatalf("ArchiveInstanceCommand = %+v", captured)
	}
	if !strings.Contains(recorder.Body.String(), `"status":"archived"`) {
		t.Fatalf("ArchiveInstance() response = %s", recorder.Body.String())
	}
}

func TestArchiveSessionMapsStateConflict(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	sessionID := apiUUID(233)
	catalog := lifecycleArchiveCatalogStub{
		archiveSession: func(context.Context, xiangwanadmin.ArchiveSessionCommand) (activity.Session, error) {
			return activity.Session{}, xiangwanadmin.ErrArchiveNotAllowed
		},
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/admin/sessions/"+sessionID.String()+"/archives",
		strings.NewReader(`{"expected_session_version":4}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("ArchiveSession() status = %d body = %s, want 400", recorder.Code, recorder.Body.String())
	}
}

func TestArchiveSessionRejectsMalformedIDBeforeCatalog(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	called := false
	catalog := lifecycleArchiveCatalogStub{
		archiveSession: func(context.Context, xiangwanadmin.ArchiveSessionCommand) (activity.Session, error) {
			called = true
			return activity.Session{}, nil
		},
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/admin/sessions/not-a-uuid/archives",
		strings.NewReader(`{"expected_session_version":4}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("ArchiveSession() status = %d called = %t, want 400 without catalog", recorder.Code, called)
	}
}
