package xiangwanapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
)

type auditEventCatalog struct {
	xiangwanadmin.Catalog
	read func(context.Context, xiangwanadmin.Principal, xiangwanadmin.AuditEventFilter) (xiangwanadmin.AuditEventPage, error)
}

func (catalog auditEventCatalog) ListAuditEvents(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	filter xiangwanadmin.AuditEventFilter,
) (xiangwanadmin.AuditEventPage, error) {
	return catalog.read(ctx, principal, filter)
}

func TestListAuditEventsRequiresSnapshotOnLaterPages(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	called := false
	catalog := auditEventCatalog{read: func(
		context.Context, xiangwanadmin.Principal, xiangwanadmin.AuditEventFilter,
	) (xiangwanadmin.AuditEventPage, error) {
		called = true
		return xiangwanadmin.AuditEventPage{}, nil
	}}
	for _, suffix := range []string{"?page=2", "?as_of=invalid"} {
		result := serveAdminCatalog(catalog, &principal, httptest.NewRequest(
			http.MethodGet, "/api/v1/xiangwan/admin/audit-events"+suffix, nil,
		))
		if result.Code != http.StatusBadRequest || called {
			t.Fatalf("audit query %q status/called = %d/%t, want 400/false", suffix, result.Code, called)
		}
	}
}

func TestListAuditEventsProjectsMetadataOnly(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	asOf := time.Date(2026, 9, 30, 2, 3, 4, 0, time.UTC)
	catalog := auditEventCatalog{read: func(
		_ context.Context, actor xiangwanadmin.Principal, filter xiangwanadmin.AuditEventFilter,
	) (xiangwanadmin.AuditEventPage, error) {
		if actor.PrincipalID != principal.PrincipalID || filter.Page != 2 ||
			filter.PageSize != 10 || filter.AsOf == nil || !filter.AsOf.Equal(asOf) {
			t.Fatalf("unexpected audit query filter: %+v", filter)
		}
		return xiangwanadmin.AuditEventPage{
			Items: []xiangwanadmin.AuditEvent{{
				ID: apiUUID(231), ActorID: principal.PrincipalID,
				ActionCode: "registration.answer_detail_read", TargetType: "registration",
				TargetID: apiUUID(232), OccurredAt: asOf.Add(-time.Minute),
			}},
			Page: 2, PageSize: 10, Total: 11, AsOf: asOf,
		}, nil
	}}
	result := serveAdminCatalog(catalog, &principal, httptest.NewRequest(http.MethodGet,
		"/api/v1/xiangwan/admin/audit-events?page=2&page_size=10&as_of="+
			asOf.Format(time.RFC3339Nano), nil))
	if result.Code != http.StatusOK || result.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("audit query status/cache = %d/%q", result.Code, result.Header().Get("Cache-Control"))
	}
	body := result.Body.String()
	if !strings.Contains(body, "registration.answer_detail_read") ||
		strings.Contains(body, "details") || strings.Contains(body, "answer_values") ||
		strings.Contains(body, "request_id") {
		t.Fatalf("audit response leaked details or omitted metadata: %s", body)
	}
}
