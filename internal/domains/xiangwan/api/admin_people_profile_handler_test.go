package xiangwanapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

type peopleProfileCatalogStub struct {
	xiangwanadmin.Catalog
	create func(context.Context, xiangwanadmin.CreatePeopleProfileCommand) (xiangwanadmin.Person, error)
	update func(context.Context, xiangwanadmin.UpdatePeopleProfileCommand) (xiangwanadmin.Person, error)
	review func(context.Context, xiangwanadmin.ReviewPeopleProfileCommand) (xiangwanadmin.Person, error)
}

func (stub peopleProfileCatalogStub) CreatePeopleProfile(ctx context.Context, command xiangwanadmin.CreatePeopleProfileCommand) (xiangwanadmin.Person, error) {
	return stub.create(ctx, command)
}

func (stub peopleProfileCatalogStub) UpdatePeopleProfile(ctx context.Context, command xiangwanadmin.UpdatePeopleProfileCommand) (xiangwanadmin.Person, error) {
	return stub.update(ctx, command)
}

func (stub peopleProfileCatalogStub) ReviewPeopleProfile(ctx context.Context, command xiangwanadmin.ReviewPeopleProfileCommand) (xiangwanadmin.Person, error) {
	return stub.review(ctx, command)
}

func testPerson(id uuid.UUID) xiangwanadmin.Person {
	return xiangwanadmin.Person{
		ID: id, DisplayName: "主理人", Introduction: "活动介绍", ProfileStatus: "published",
		ModerationStatus: "approved", Version: 2,
		UpdatedAt: time.Date(2026, time.September, 28, 8, 0, 0, 0, time.UTC),
	}
}

func TestCreatePeopleProfilePassesPublicContentOnly(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	operationID := uuid.New()
	var captured xiangwanadmin.CreatePeopleProfileCommand
	catalog := peopleProfileCatalogStub{
		create: func(_ context.Context, command xiangwanadmin.CreatePeopleProfileCommand) (xiangwanadmin.Person, error) {
			captured = command
			return testPerson(apiUUID(210)), nil
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/xiangwan/admin/people", strings.NewReader(`{"display_name":"主理人","headline":"AI 顾问","introduction":"活动介绍"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID.String())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("CreatePeopleProfile() status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if captured.DisplayName != "主理人" || captured.Headline != "AI 顾问" || captured.Introduction != "活动介绍" ||
		captured.OperationID != operationID || captured.ActorID != principal.PrincipalID || captured.IdentityLinkID != principal.IdentityLinkID || captured.RequestID == "" {
		t.Fatalf("CreatePeopleProfile command = %+v", captured)
	}
}

func TestUpdatePeopleProfileMapsVersionConflict(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	catalog := peopleProfileCatalogStub{
		update: func(context.Context, xiangwanadmin.UpdatePeopleProfileCommand) (xiangwanadmin.Person, error) {
			return xiangwanadmin.Person{}, xiangwanadmin.ErrVersionConflict
		},
	}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/xiangwan/admin/people/"+apiUUID(211).String(), strings.NewReader(`{"expected_version":2,"display_name":"主理人","headline":"AI 顾问","introduction":"活动介绍"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("UpdatePeopleProfile() status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestReviewPeopleProfilePassesExpectedVersionAndDecision(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	profileID := apiUUID(212)
	operationID := uuid.New()
	var captured xiangwanadmin.ReviewPeopleProfileCommand
	catalog := peopleProfileCatalogStub{
		review: func(_ context.Context, command xiangwanadmin.ReviewPeopleProfileCommand) (xiangwanadmin.Person, error) {
			captured = command
			value := testPerson(profileID)
			value.Version = 3
			return value, nil
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/xiangwan/admin/people/"+profileID.String()+"/reviews", strings.NewReader(`{"expected_version":2,"decision":"approved"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID.String())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("ReviewPeopleProfile() status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if captured.PeopleProfileID != profileID || captured.ExpectedVersion != 2 || captured.Decision != "approved" || captured.OperationID != operationID {
		t.Fatalf("ReviewPeopleProfile command = %+v", captured)
	}
}
