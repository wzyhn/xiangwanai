package xiangwanapi

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/google/uuid"
)

func TestPublicPeopleServiceFixesTenantAndClock(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	asOf := time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC)
	first := knownPublicPerson(t, tenantID, asOf.Add(-time.Minute))
	second := knownPublicPerson(t, tenantID, asOf.Add(-2*time.Minute))
	reader := &fakePublicPeopleReader{page: people.PublicProfilesPage{
		Items:      []people.Profile{first, second},
		AsOf:       asOf,
		NextCursor: "opaque-cursor",
	}}
	service, err := NewPublicPeopleService(tenantID, reader, func() time.Time {
		return asOf
	})
	if err != nil {
		t.Fatalf("NewPublicPeopleService() error = %v", err)
	}
	request := PublicPeopleRequest{Cursor: "incoming-cursor", Limit: 2}
	page, err := service.List(context.Background(), request)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !reflect.DeepEqual(page, reader.page) || reader.calls != 1 ||
		reader.filter.TenantID != tenantID ||
		reader.filter.Cursor != request.Cursor ||
		reader.filter.Limit != request.Limit ||
		!reader.filter.At.Equal(asOf) {
		t.Fatalf("page=%+v reader=%+v", page, reader)
	}
}

func TestPublicPeopleServiceReadsExactPublishedProfile(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	asOf := time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC)
	profile := knownPublicPerson(t, tenantID, asOf.Add(-time.Minute))
	reader := &fakePublicPeopleReader{profile: profile}
	service, err := NewPublicPeopleService(tenantID, reader, time.Now)
	if err != nil {
		t.Fatalf("NewPublicPeopleService() error = %v", err)
	}
	got, err := service.Read(context.Background(), profile.ID)
	if err != nil || !reflect.DeepEqual(got, profile) || reader.calls != 1 ||
		reader.tenantID != tenantID || reader.peopleID != profile.ID {
		t.Fatalf("Read() = %+v, %v reader=%+v", got, err, reader)
	}
}

func TestPublicPeopleServiceRejectsCrossTenantAndMissingFacts(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	asOf := time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC)
	profile := knownPublicPerson(t, uuid.New(), asOf.Add(-time.Minute))
	reader := &fakePublicPeopleReader{
		page:    people.PublicProfilesPage{Items: []people.Profile{profile}, AsOf: asOf},
		profile: profile,
		err:     peoplepostgres.ErrProfileNotFound,
	}
	service, err := NewPublicPeopleService(tenantID, reader, func() time.Time {
		return asOf
	})
	if err != nil {
		t.Fatalf("NewPublicPeopleService() error = %v", err)
	}

	reader.err = nil
	if _, err := service.List(
		context.Background(),
		PublicPeopleRequest{},
	); !errors.Is(err, ErrPublicPeopleResponseConflict) {
		t.Fatalf("List(cross tenant) error = %v", err)
	}
	if _, err := service.Read(
		context.Background(),
		profile.ID,
	); !errors.Is(err, ErrPublicPeopleResponseConflict) {
		t.Fatalf("Read(cross tenant) error = %v", err)
	}

	reader.err = peoplepostgres.ErrProfileNotFound
	if _, err := service.Read(
		context.Background(),
		uuid.New(),
	); !errors.Is(err, peoplepostgres.ErrProfileNotFound) {
		t.Fatalf("Read(missing) error = %v", err)
	}
	if _, err := service.Read(
		context.Background(),
		uuid.Nil,
	); !errors.Is(err, ErrInvalidPublicPeopleRequest) {
		t.Fatalf("Read(nil) error = %v", err)
	}
}

func knownPublicPerson(
	t *testing.T,
	tenantID uuid.UUID,
	at time.Time,
) people.Profile {
	t.Helper()
	draft, err := people.NewProfile(people.NewProfileCommand{
		TenantID:     tenantID,
		DisplayName:  "Lin",
		Headline:     "Community host",
		Introduction: "Builds useful gatherings.",
		ActorID:      uuid.New(),
		At:           at.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("NewProfile() error = %v", err)
	}
	published, err := people.ReviewProfile(draft, people.ReviewProfileCommand{
		Decision: people.ModerationStatusApproved,
		ActorID:  uuid.New(),
		At:       at,
	})
	if err != nil {
		t.Fatalf("ReviewProfile() error = %v", err)
	}
	return published
}

type fakePublicPeopleReader struct {
	page    people.PublicProfilesPage
	profile people.Profile
	err     error

	calls    int
	filter   people.PublicProfileFilter
	tenantID uuid.UUID
	peopleID uuid.UUID
}

func (fake *fakePublicPeopleReader) ListPublishedProfiles(
	_ context.Context,
	filter people.PublicProfileFilter,
) (people.PublicProfilesPage, error) {
	fake.calls++
	fake.filter = filter
	return fake.page, fake.err
}

func (fake *fakePublicPeopleReader) GetPublishedProfile(
	_ context.Context,
	tenantID uuid.UUID,
	peopleID uuid.UUID,
) (people.Profile, error) {
	fake.calls++
	fake.tenantID = tenantID
	fake.peopleID = peopleID
	return fake.profile, fake.err
}
