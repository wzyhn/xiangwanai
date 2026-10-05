package xiangwanapi

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	"github.com/google/uuid"
)

func TestMyFavoritesServiceBindsOwnerAndSnapshotTime(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(230)
	principalID := apiUUID(231)
	now := time.Date(2026, time.September, 25, 9, 10, 11, 0, time.UTC)
	wantPage := activity.MyFavoritesPage{
		Items: []activity.MyFavoriteItem{{
			SeriesID:          apiUUID(232),
			Title:             "AI Salon",
			SeriesStatus:      activity.SeriesStatusActive,
			FavoriteCount:     7,
			FavoritedAt:       now.Add(-time.Minute),
			SessionsAvailable: true,
		}},
		AsOf: now,
	}
	reader := &fakeMyFavoritesReader{page: wantPage}
	service, err := NewMyFavoritesService(
		tenantID,
		reader,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewMyFavoritesService() error = %v", err)
	}
	request := MyFavoritesRequest{Cursor: "opaque-owner-cursor", Limit: 25}
	page, err := service.Read(context.Background(), principalID, request)
	if err != nil || !reflect.DeepEqual(page, wantPage) {
		t.Fatalf("Read() = %+v, %v", page, err)
	}
	wantFilter := activity.MyFavoriteFilter{
		TenantID:    tenantID,
		PrincipalID: principalID,
		Limit:       request.Limit,
		Cursor:      request.Cursor,
		At:          now,
	}
	if reader.calls != 1 || !reflect.DeepEqual(reader.filter, wantFilter) {
		t.Fatalf("reader calls=%d filter=%+v", reader.calls, reader.filter)
	}
}

func TestMyFavoritesServiceValidatesBoundariesAndPreservesReaderCause(t *testing.T) {
	t.Parallel()

	if _, err := NewMyFavoritesService(uuid.Nil, nil, nil); !errors.Is(
		err,
		ErrInvalidMyFavoritesService,
	) {
		t.Fatalf("NewMyFavoritesService(invalid) error = %v", err)
	}
	reader := &fakeMyFavoritesReader{}
	service, err := NewMyFavoritesService(apiUUID(233), reader, time.Now)
	if err != nil {
		t.Fatalf("NewMyFavoritesService() error = %v", err)
	}
	for _, test := range []struct {
		name        string
		principalID uuid.UUID
		request     MyFavoritesRequest
	}{
		{name: "missing principal"},
		{name: "negative limit", principalID: apiUUID(234), request: MyFavoritesRequest{Limit: -1}},
		{name: "excessive limit", principalID: apiUUID(235), request: MyFavoritesRequest{Limit: activity.MaxMyFavoritesLimit + 1}},
		{name: "long cursor", principalID: apiUUID(236), request: MyFavoritesRequest{Cursor: string(make([]byte, 2049))}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, gotErr := service.Read(
				context.Background(),
				test.principalID,
				test.request,
			); !errors.Is(gotErr, ErrInvalidMyFavoritesRequest) {
				t.Fatalf("Read(invalid) error = %v", gotErr)
			}
		})
	}
	if reader.calls != 0 {
		t.Fatalf("invalid requests reached reader %d times", reader.calls)
	}

	wantErr := activitypostgres.ErrStaleMyFavoritesCursor
	failingReader := &fakeMyFavoritesReader{err: wantErr}
	failingService, err := NewMyFavoritesService(
		apiUUID(237),
		failingReader,
		time.Now,
	)
	if err != nil {
		t.Fatalf("NewMyFavoritesService(failing) error = %v", err)
	}
	if _, gotErr := failingService.Read(
		context.Background(),
		apiUUID(238),
		MyFavoritesRequest{},
	); !errors.Is(gotErr, wantErr) {
		t.Fatalf("Read(failure) error = %v", gotErr)
	}
}

func TestMyFavoritesServiceRejectsInvalidReaderProjection(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	reader := &fakeMyFavoritesReader{page: activity.MyFavoritesPage{
		Items:      []activity.MyFavoriteItem{},
		AsOf:       now,
		NextCursor: "cursor-without-items",
	}}
	service, err := NewMyFavoritesService(
		apiUUID(239),
		reader,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewMyFavoritesService() error = %v", err)
	}
	if _, gotErr := service.Read(
		context.Background(),
		apiUUID(240),
		MyFavoritesRequest{},
	); !errors.Is(gotErr, ErrInvalidMyFavoritesResponse) {
		t.Fatalf("Read(invalid projection) error = %v", gotErr)
	}
}

type fakeMyFavoritesReader struct {
	page activity.MyFavoritesPage
	err  error

	calls  int
	filter activity.MyFavoriteFilter
}

func (reader *fakeMyFavoritesReader) ListMyFavorites(
	_ context.Context,
	filter activity.MyFavoriteFilter,
) (activity.MyFavoritesPage, error) {
	reader.calls++
	reader.filter = filter
	return reader.page, reader.err
}
