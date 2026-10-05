package xiangwanapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

type peopleRoleCatalogStub struct {
	xiangwanadmin.Catalog
	listPeople func(context.Context, xiangwanadmin.Principal, int, int) (xiangwanadmin.PeoplePage, error)
	listRoles  func(context.Context, xiangwanadmin.Principal, uuid.UUID) ([]xiangwanadmin.InstanceRole, error)
	assignRole func(context.Context, xiangwanadmin.AssignInstanceRoleCommand) (xiangwanadmin.InstanceRole, error)
	revokeRole func(context.Context, xiangwanadmin.RevokeInstanceRoleCommand) (xiangwanadmin.InstanceRole, error)
}

func (stub peopleRoleCatalogStub) ListPeople(ctx context.Context, principal xiangwanadmin.Principal, page, pageSize int) (xiangwanadmin.PeoplePage, error) {
	return stub.listPeople(ctx, principal, page, pageSize)
}
func (stub peopleRoleCatalogStub) ListInstanceRoles(ctx context.Context, principal xiangwanadmin.Principal, instanceID uuid.UUID) ([]xiangwanadmin.InstanceRole, error) {
	return stub.listRoles(ctx, principal, instanceID)
}
func (stub peopleRoleCatalogStub) AssignInstanceRole(ctx context.Context, command xiangwanadmin.AssignInstanceRoleCommand) (xiangwanadmin.InstanceRole, error) {
	return stub.assignRole(ctx, command)
}
func (stub peopleRoleCatalogStub) RevokeInstanceRole(ctx context.Context, command xiangwanadmin.RevokeInstanceRoleCommand) (xiangwanadmin.InstanceRole, error) {
	return stub.revokeRole(ctx, command)
}

func TestListPeopleProjectsSafeProfileProjection(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	profileID := apiUUID(91)
	called := false
	catalog := peopleRoleCatalogStub{
		listPeople: func(_ context.Context, got xiangwanadmin.Principal, page, pageSize int) (xiangwanadmin.PeoplePage, error) {
			called = true
			if got.PrincipalID != principal.PrincipalID || page != 1 || pageSize != 20 {
				t.Fatalf("ListPeople args = %+v page=%d size=%d", got, page, pageSize)
			}
			return xiangwanadmin.PeoplePage{
				Items: []xiangwanadmin.Person{{
					ID: profileID, DisplayName: "主理人", Introduction: "带你认识新朋友",
					ProfileStatus: "published", ModerationStatus: "approved", Version: 2,
					UpdatedAt:       time.Date(2026, time.September, 28, 0, 0, 0, 0, time.UTC),
					ActiveBindingID: func() *uuid.UUID { value := apiUUID(92); return &value }(),
				}},
				Page: 1, PageSize: 20, Total: 1,
			}, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/admin/people?page=1&page_size=20", nil)
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusOK || !called {
		t.Fatalf("ListPeople status=%d called=%t body=%s", recorder.Code, called, recorder.Body.String())
	}
	var envelope struct {
		Code int                     `json:"code"`
		Data AdminPeoplePageResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode ListPeople: %v", err)
	}
	if envelope.Code != 0 || len(envelope.Data.Items) != 1 || !envelope.Data.Items[0].HasActiveBinding ||
		envelope.Data.Items[0].ID != profileID.String() || strings.Contains(recorder.Body.String(), principal.PrincipalID.String()) {
		t.Fatalf("safe People response = %s", recorder.Body.String())
	}
}

func TestAssignInstanceRoleUsesPeopleProfileSelector(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	instanceID, profileID, operationID := apiUUID(93), apiUUID(94), uuid.New()
	var captured xiangwanadmin.AssignInstanceRoleCommand
	catalog := peopleRoleCatalogStub{
		assignRole: func(_ context.Context, command xiangwanadmin.AssignInstanceRoleCommand) (xiangwanadmin.InstanceRole, error) {
			captured = command
			return xiangwanadmin.InstanceRole{
				ID: apiUUID(95), InstanceID: instanceID, PeopleProfileID: profileID,
				DisplayName: "主理人", RoleCode: "host", RoleStatus: "active",
				GrantReason: command.GrantReason, Version: 1,
				GrantedAt: time.Date(2026, time.September, 28, 0, 0, 0, 0, time.UTC),
			}, nil
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/xiangwan/admin/instances/"+instanceID.String()+"/roles", strings.NewReader(`{"people_profile_id":"`+profileID.String()+`","role_code":"host","grant_reason":"本期活动主理"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID.String())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("AssignInstanceRole status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if captured.OperationID != operationID || captured.InstanceID != instanceID || captured.PeopleProfileID != profileID ||
		captured.RoleCode != "host" || captured.GrantReason != "本期活动主理" || captured.ActorID != principal.PrincipalID {
		t.Fatalf("captured AssignInstanceRole command = %+v", captured)
	}
}
