package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestPublicPeopleHandlerReturnsOnlyPublicProfileFacts(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC)
	profile := knownPublicPerson(t, apiUUID(230), asOf.Add(-time.Minute))
	service := &fakePublicPeopleApplication{page: people.PublicProfilesPage{
		Items:      []people.Profile{profile},
		AsOf:       asOf,
		NextCursor: "next-public-cursor",
	}}
	engine := gin.New()
	NewPublicPeopleHandler(service).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/people?limit=10",
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET People status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                  `json:"code"`
		Data PublicPeopleResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode public People response: %v", err)
	}
	if envelope.Code != 0 || len(envelope.Data.Items) != 1 ||
		envelope.Data.Items[0].PeopleID != profile.ID.String() ||
		envelope.Data.Items[0].DisplayName != profile.DisplayName ||
		envelope.Data.Items[0].Headline == nil ||
		*envelope.Data.Items[0].Headline != *profile.Headline ||
		envelope.Data.NextCursor != "next-public-cursor" ||
		service.listCalls != 1 || service.request.Limit != 10 {
		t.Fatalf("response=%+v service=%+v", envelope.Data, service)
	}
	for _, forbidden := range []string{
		"tenant_id",
		"principal_id",
		"moderated_by",
		"created_by",
		"updated_by",
		"profile_status",
		"moderation_status",
		profile.TenantID.String(),
		profile.ModeratedBy.String(),
		profile.CreatedBy.String(),
		profile.UpdatedBy.String(),
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("private field %q crossed HTTP boundary: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestPublicPeopleHandlerReadsExactUnboundProfile(t *testing.T) {
	t.Parallel()

	profile := knownPublicPerson(
		t,
		apiUUID(231),
		time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC),
	)
	service := &fakePublicPeopleApplication{profile: profile}
	engine := gin.New()
	NewPublicPeopleHandler(service).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/people/"+profile.ID.String(),
			nil,
		),
	)
	if recorder.Code != http.StatusOK || service.readCalls != 1 ||
		service.peopleID != profile.ID ||
		!strings.Contains(recorder.Body.String(), `"person"`) ||
		!strings.Contains(recorder.Body.String(), profile.ID.String()) {
		t.Fatalf("GET Person status=%d service=%+v body=%s", recorder.Code, service, recorder.Body.String())
	}
}

func TestPublicPeopleHandlerRejectsAmbiguousInputs(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"/api/v1/xiangwan/people?unknown=value",
		"/api/v1/xiangwan/people?cursor=one&cursor=two",
		"/api/v1/xiangwan/people?cursor=%20opaque",
		"/api/v1/xiangwan/people?limit=0",
		"/api/v1/xiangwan/people?limit=01",
		"/api/v1/xiangwan/people?limit=101",
		"/api/v1/xiangwan/people/not-a-uuid",
		"/api/v1/xiangwan/people/" + strings.ToUpper(apiUUID(232).String()),
		"/api/v1/xiangwan/people/" + apiUUID(233).String() + "?expand=binding",
	} {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			service := &fakePublicPeopleApplication{}
			engine := gin.New()
			NewPublicPeopleHandler(service).RegisterRoutes(
				engine.Group("/api/v1/xiangwan"),
			)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, path, nil),
			)
			if recorder.Code != http.StatusBadRequest ||
				service.listCalls != 0 || service.readCalls != 0 {
				t.Fatalf("status=%d service=%+v body=%s", recorder.Code, service, recorder.Body.String())
			}
		})
	}
}

func TestPublicPeopleHandlerMapsStableErrorsAndEmptyState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		serviceErr error
		wantStatus int
	}{
		{name: "invalid cursor", path: "/api/v1/xiangwan/people", serviceErr: peoplepostgres.ErrInvalidPublicProfilesCursor, wantStatus: http.StatusBadRequest},
		{name: "stale cursor", path: "/api/v1/xiangwan/people", serviceErr: peoplepostgres.ErrStalePublicProfilesCursor, wantStatus: http.StatusConflict},
		{name: "missing detail", path: "/api/v1/xiangwan/people/" + apiUUID(234).String(), serviceErr: peoplepostgres.ErrProfileNotFound, wantStatus: http.StatusNotFound},
		{name: "reader failure", path: "/api/v1/xiangwan/people", serviceErr: errors.New("private database address"), wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakePublicPeopleApplication{err: test.serviceErr}
			engine := gin.New()
			NewPublicPeopleHandler(service).RegisterRoutes(
				engine.Group("/api/v1/xiangwan"),
			)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, test.path, nil),
			)
			if recorder.Code != test.wantStatus ||
				strings.Contains(recorder.Body.String(), "database address") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}

	service := &fakePublicPeopleApplication{page: people.PublicProfilesPage{
		Items: []people.Profile{},
		AsOf:  time.Now().UTC(),
	}}
	engine := gin.New()
	NewPublicPeopleHandler(service).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/people", nil),
	)
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"items":[]`) ||
		!strings.Contains(recorder.Body.String(), `"empty_state":"no_people"`) {
		t.Fatalf("empty status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

type fakePublicPeopleApplication struct {
	page    people.PublicProfilesPage
	profile people.Profile
	err     error

	listCalls int
	readCalls int
	request   PublicPeopleRequest
	peopleID  uuid.UUID
}

func (fake *fakePublicPeopleApplication) List(
	_ context.Context,
	request PublicPeopleRequest,
) (people.PublicProfilesPage, error) {
	fake.listCalls++
	fake.request = request
	return fake.page, fake.err
}

func (fake *fakePublicPeopleApplication) Read(
	_ context.Context,
	peopleID uuid.UUID,
) (people.Profile, error) {
	fake.readCalls++
	fake.peopleID = peopleID
	return fake.profile, fake.err
}
