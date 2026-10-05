package xiangwanapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	"github.com/google/uuid"
)

var (
	ErrInvalidSeriesFavoriteService = errors.New(
		"invalid xiangwan Series favorite service",
	)
	ErrInvalidSeriesFavoriteRequest = errors.New(
		"invalid xiangwan Series favorite request",
	)
)

type seriesFavoriteSetter interface {
	Set(
		context.Context,
		activitypostgres.SetSeriesFavoriteCommand,
	) (activity.SeriesFavoriteState, error)
}

type SeriesFavoriteService struct {
	tenantID uuid.UUID
	writer   seriesFavoriteSetter
}

func NewSeriesFavoriteService(
	tenantID uuid.UUID,
	writer seriesFavoriteSetter,
) (*SeriesFavoriteService, error) {
	if tenantID == uuid.Nil || writer == nil {
		return nil, ErrInvalidSeriesFavoriteService
	}
	return &SeriesFavoriteService{tenantID: tenantID, writer: writer}, nil
}

func (service *SeriesFavoriteService) Set(
	ctx context.Context,
	principalID uuid.UUID,
	seriesID uuid.UUID,
	favorited bool,
) (activity.SeriesFavoriteState, error) {
	if service == nil || service.tenantID == uuid.Nil || service.writer == nil {
		return activity.SeriesFavoriteState{}, ErrInvalidSeriesFavoriteService
	}
	if ctx == nil || principalID == uuid.Nil || seriesID == uuid.Nil {
		return activity.SeriesFavoriteState{}, ErrInvalidSeriesFavoriteRequest
	}
	state, err := service.writer.Set(
		ctx,
		activitypostgres.SetSeriesFavoriteCommand{
			TenantID:    service.tenantID,
			PrincipalID: principalID,
			SeriesID:    seriesID,
			Favorited:   favorited,
		},
	)
	if err != nil {
		return activity.SeriesFavoriteState{}, fmt.Errorf(
			"set xiangwan Series favorite: %w",
			err,
		)
	}
	return state, nil
}
