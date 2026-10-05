package xiangwanapi

import (
	"context"
	"errors"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	"github.com/google/uuid"
)

func TestSeriesFavoriteServiceBindsRuntimeTenantAndPrincipal(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(170)
	principalID := apiUUID(171)
	seriesID := apiUUID(172)
	written := activity.SeriesFavoriteState{Favorited: true}
	writer := &fakeSeriesFavoriteSetter{state: written}
	service, err := NewSeriesFavoriteService(tenantID, writer)
	if err != nil {
		t.Fatalf("NewSeriesFavoriteService() error = %v", err)
	}
	state, err := service.Set(
		context.Background(),
		principalID,
		seriesID,
		true,
	)
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if state.Favorited != written.Favorited || writer.calls != 1 ||
		writer.command.TenantID != tenantID ||
		writer.command.PrincipalID != principalID ||
		writer.command.SeriesID != seriesID || !writer.command.Favorited {
		t.Fatalf("state=%+v writer=%+v", state, writer)
	}
}

func TestSeriesFavoriteServiceValidatesAndPreservesWriterError(t *testing.T) {
	t.Parallel()

	if _, err := NewSeriesFavoriteService(uuid.Nil, &fakeSeriesFavoriteSetter{}); !errors.Is(
		err,
		ErrInvalidSeriesFavoriteService,
	) {
		t.Fatalf("nil tenant error = %v", err)
	}
	if _, err := NewSeriesFavoriteService(apiUUID(173), nil); !errors.Is(
		err,
		ErrInvalidSeriesFavoriteService,
	) {
		t.Fatalf("nil writer error = %v", err)
	}
	writer := &fakeSeriesFavoriteSetter{
		err: activitypostgres.ErrSeriesFavoriteTransactionConflict,
	}
	service, err := NewSeriesFavoriteService(apiUUID(174), writer)
	if err != nil {
		t.Fatalf("NewSeriesFavoriteService() error = %v", err)
	}
	if _, err := service.Set(
		context.Background(),
		uuid.Nil,
		apiUUID(175),
		true,
	); !errors.Is(err, ErrInvalidSeriesFavoriteRequest) {
		t.Fatalf("invalid request error = %v", err)
	}
	if _, err := service.Set(
		context.Background(),
		apiUUID(176),
		apiUUID(177),
		false,
	); !errors.Is(err, activitypostgres.ErrSeriesFavoriteTransactionConflict) {
		t.Fatalf("writer error = %v", err)
	}
}

type fakeSeriesFavoriteSetter struct {
	state activity.SeriesFavoriteState
	err   error

	calls   int
	command activitypostgres.SetSeriesFavoriteCommand
}

func (fake *fakeSeriesFavoriteSetter) Set(
	_ context.Context,
	command activitypostgres.SetSeriesFavoriteCommand,
) (activity.SeriesFavoriteState, error) {
	fake.calls++
	fake.command = command
	return fake.state, fake.err
}
