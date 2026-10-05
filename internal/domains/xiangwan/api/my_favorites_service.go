package xiangwanapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

var (
	ErrInvalidMyFavoritesService = errors.New(
		"invalid xiangwan My Favorites service",
	)
	ErrInvalidMyFavoritesRequest = errors.New(
		"invalid xiangwan My Favorites request",
	)
	ErrInvalidMyFavoritesResponse = errors.New(
		"invalid xiangwan My Favorites response",
	)
)

type myFavoritesReader interface {
	ListMyFavorites(
		context.Context,
		activity.MyFavoriteFilter,
	) (activity.MyFavoritesPage, error)
}

type MyFavoritesRequest struct {
	Cursor string
	Limit  int
}

type MyFavoritesService struct {
	tenantID uuid.UUID
	reader   myFavoritesReader
	clock    func() time.Time
}

func NewMyFavoritesService(
	tenantID uuid.UUID,
	reader myFavoritesReader,
	clock func() time.Time,
) (*MyFavoritesService, error) {
	if tenantID == uuid.Nil || reader == nil || clock == nil {
		return nil, ErrInvalidMyFavoritesService
	}
	return &MyFavoritesService{
		tenantID: tenantID,
		reader:   reader,
		clock:    clock,
	}, nil
}

func (service *MyFavoritesService) Read(
	ctx context.Context,
	principalID uuid.UUID,
	request MyFavoritesRequest,
) (activity.MyFavoritesPage, error) {
	if service == nil || service.tenantID == uuid.Nil || service.reader == nil ||
		service.clock == nil {
		return activity.MyFavoritesPage{}, ErrInvalidMyFavoritesService
	}
	if ctx == nil || principalID == uuid.Nil || request.Limit < 0 ||
		request.Limit > activity.MaxMyFavoritesLimit || len(request.Cursor) > 2048 {
		return activity.MyFavoritesPage{}, ErrInvalidMyFavoritesRequest
	}
	at := service.clock().UTC()
	if at.IsZero() {
		return activity.MyFavoritesPage{}, ErrInvalidMyFavoritesService
	}
	page, err := service.reader.ListMyFavorites(ctx, activity.MyFavoriteFilter{
		TenantID:    service.tenantID,
		PrincipalID: principalID,
		Limit:       request.Limit,
		Cursor:      request.Cursor,
		At:          at,
	})
	if err != nil {
		return activity.MyFavoritesPage{}, fmt.Errorf(
			"read xiangwan My Favorites: %w",
			err,
		)
	}
	if err := validateMyFavoritesPage(page, request.Limit, at); err != nil {
		return activity.MyFavoritesPage{}, err
	}
	return page, nil
}

func validateMyFavoritesPage(
	page activity.MyFavoritesPage,
	requestedLimit int,
	at time.Time,
) error {
	limit := requestedLimit
	if limit == 0 {
		limit = activity.DefaultMyFavoritesLimit
	}
	if page.Items == nil || page.AsOf.IsZero() ||
		page.AsOf.After(at.Add(activity.MyFavoritesFutureSkew)) ||
		len(page.Items) > limit || len(page.NextCursor) > 2048 ||
		(len(page.Items) == 0 && page.NextCursor != "") {
		return ErrInvalidMyFavoritesResponse
	}
	seen := make(map[uuid.UUID]struct{}, len(page.Items))
	for _, item := range page.Items {
		if err := activity.ValidateMyFavoriteItem(item); err != nil ||
			item.FavoritedAt.After(page.AsOf) {
			return ErrInvalidMyFavoritesResponse
		}
		if _, duplicate := seen[item.SeriesID]; duplicate {
			return ErrInvalidMyFavoritesResponse
		}
		seen[item.SeriesID] = struct{}{}
	}
	return nil
}
