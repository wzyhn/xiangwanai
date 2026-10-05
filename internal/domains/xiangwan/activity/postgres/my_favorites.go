package activitypostgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

var (
	ErrInvalidMyFavoritesFilter = errors.New(
		"invalid xiangwan My Favorites filter",
	)
	ErrInvalidMyFavoritesCursor = errors.New(
		"invalid xiangwan My Favorites cursor",
	)
	ErrStaleMyFavoritesCursor = errors.New(
		"stale xiangwan My Favorites cursor",
	)
	ErrMyFavoriteProjection = errors.New(
		"xiangwan My Favorite projection mismatch",
	)
)

type decodedMyFavoritesCursor struct {
	Version     int       `json:"v"`
	OwnerScope  string    `json:"owner_scope"`
	AsOf        time.Time `json:"as_of"`
	FavoritedAt time.Time `json:"favorited_at"`
	SeriesID    uuid.UUID `json:"series_id"`
}

func (repository *Repository) ListMyFavorites(
	ctx context.Context,
	filter activity.MyFavoriteFilter,
) (activity.MyFavoritesPage, error) {
	if repository == nil || repository.db == nil || ctx == nil {
		return activity.MyFavoritesPage{}, ErrInvalidMyFavoritesFilter
	}
	normalized, cursor, err := normalizeMyFavoritesFilter(filter)
	if err != nil {
		return activity.MyFavoritesPage{}, err
	}
	query := `
SELECT
    activity_series.id,
    activity_series.title,
    activity_series.status,
    activity_series.favorite_count,
    COALESCE(
        (
            SELECT TO_JSON(ARRAY(
                SELECT public_principal.avatar_url
                FROM xiangwan_series_favorites AS public_favorite
                JOIN principals AS public_principal
                  ON public_principal.id = public_favorite.principal_id
                JOIN xiangwan_consumer_profiles AS public_profile
                  ON public_profile.tenant_id = public_favorite.tenant_id
                 AND public_profile.principal_id = public_favorite.principal_id
                WHERE public_favorite.tenant_id = activity_series.tenant_id
                  AND public_favorite.series_id = activity_series.id
                  AND public_principal.status = 'active'
                  AND public_principal.deleted_at IS NULL
                  AND public_principal.avatar_url <> ''
                  AND (
                      public_profile.occupation_public
                      OR public_profile.introduction_public
                      OR public_profile.tags_public
                  )
                ORDER BY public_favorite.created_at DESC,
                         public_favorite.principal_id DESC
                LIMIT 3
            ))
        ),
        '[]'::JSON
    ) AS favorite_avatars,
    favorite.created_at,
    activity_series.status = 'active'
        AND activity_series.home_visible
        AND EXISTS (
            SELECT 1
            FROM xiangwan_activity_instances AS current_instance
            WHERE current_instance.tenant_id = activity_series.tenant_id
              AND current_instance.series_id = activity_series.id
              AND current_instance.id = activity_series.current_public_instance_id
              AND current_instance.status IN ('published', 'completed', 'cancelled')
        ) AS sessions_available
FROM xiangwan_series_favorites AS favorite
JOIN xiangwan_activity_series AS activity_series
  ON activity_series.tenant_id = favorite.tenant_id
 AND activity_series.id = favorite.series_id
WHERE favorite.tenant_id = $1
  AND favorite.principal_id = $2
  AND favorite.created_at <= $3
  AND EXISTS (
      SELECT 1
      FROM principals
      WHERE id = $2
        AND status = 'active'
        AND deleted_at IS NULL
  )
`
	args := []any{
		normalized.TenantID,
		normalized.PrincipalID,
		normalized.At,
	}
	if cursor != nil {
		query += `
  AND (favorite.created_at, favorite.series_id) < ($4, $5)
`
		args = append(args, cursor.FavoritedAt, cursor.SeriesID)
	}
	query += `
ORDER BY favorite.created_at DESC, favorite.series_id DESC
`
	query += fmt.Sprintf("LIMIT $%d\n", len(args)+1)
	args = append(args, normalized.Limit+1)

	rows, err := repository.db.queryContext(ctx, query, args...)
	if err != nil {
		return activity.MyFavoritesPage{}, fmt.Errorf(
			"list xiangwan My Favorites: %w",
			err,
		)
	}
	defer func() {
		_ = rows.Close()
	}()
	items := make([]activity.MyFavoriteItem, 0, normalized.Limit+1)
	seen := make(map[uuid.UUID]struct{}, normalized.Limit+1)
	for rows.Next() {
		var item activity.MyFavoriteItem
		var favoriteAvatars StringArrayJSON
		if err := rows.Scan(
			&item.SeriesID,
			&item.Title,
			&item.SeriesStatus,
			&item.FavoriteCount,
			&favoriteAvatars,
			&item.FavoritedAt,
			&item.SessionsAvailable,
		); err != nil {
			return activity.MyFavoritesPage{}, fmt.Errorf(
				"scan xiangwan My Favorite: %w",
				err,
			)
		}
		if len(favoriteAvatars) > 0 {
			item.FavoriteAvatars = append([]string(nil), favoriteAvatars...)
		}
		item.FavoritedAt = item.FavoritedAt.UTC()
		if err := activity.ValidateMyFavoriteItem(item); err != nil {
			return activity.MyFavoritesPage{}, fmt.Errorf(
				"%w: %v",
				ErrMyFavoriteProjection,
				err,
			)
		}
		if _, duplicate := seen[item.SeriesID]; duplicate {
			return activity.MyFavoritesPage{}, fmt.Errorf(
				"%w: duplicate Favorite",
				ErrMyFavoriteProjection,
			)
		}
		seen[item.SeriesID] = struct{}{}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return activity.MyFavoritesPage{}, fmt.Errorf(
			"iterate xiangwan My Favorites: %w",
			err,
		)
	}
	nextCursor := ""
	if len(items) > normalized.Limit {
		items = items[:normalized.Limit]
		nextCursor, err = encodeMyFavoritesCursor(
			normalized,
			items[len(items)-1],
		)
		if err != nil {
			return activity.MyFavoritesPage{}, err
		}
	}
	return activity.MyFavoritesPage{
		Items:      items,
		AsOf:       normalized.At,
		NextCursor: nextCursor,
	}, nil
}

func normalizeMyFavoritesFilter(
	filter activity.MyFavoriteFilter,
) (activity.MyFavoriteFilter, *decodedMyFavoritesCursor, error) {
	if filter.TenantID == uuid.Nil || filter.PrincipalID == uuid.Nil {
		return activity.MyFavoriteFilter{}, nil, ErrInvalidMyFavoritesFilter
	}
	switch {
	case filter.Limit == 0:
		filter.Limit = activity.DefaultMyFavoritesLimit
	case filter.Limit < 1 || filter.Limit > activity.MaxMyFavoritesLimit:
		return activity.MyFavoriteFilter{}, nil, ErrInvalidMyFavoritesFilter
	}
	requestedAt := filter.At
	if requestedAt.IsZero() {
		requestedAt = time.Now()
	}
	filter.At = requestedAt.UTC()
	if filter.Cursor == "" {
		return filter, nil, nil
	}
	cursor, err := decodeMyFavoritesCursor(filter.Cursor)
	if err != nil {
		return activity.MyFavoriteFilter{}, nil, err
	}
	if cursor.OwnerScope != myFavoritesOwnerScope(
		filter.TenantID,
		filter.PrincipalID,
	) {
		return activity.MyFavoriteFilter{}, nil, ErrStaleMyFavoritesCursor
	}
	if filter.At.Sub(cursor.AsOf) > activity.MaxMyFavoritesCursorAge ||
		cursor.AsOf.After(filter.At.Add(activity.MyFavoritesFutureSkew)) {
		return activity.MyFavoriteFilter{}, nil, ErrStaleMyFavoritesCursor
	}
	filter.At = cursor.AsOf.UTC()
	return filter, &cursor, nil
}

func encodeMyFavoritesCursor(
	filter activity.MyFavoriteFilter,
	item activity.MyFavoriteItem,
) (string, error) {
	if err := activity.ValidateMyFavoriteItem(item); err != nil {
		return "", fmt.Errorf("%w: invalid item", ErrInvalidMyFavoritesCursor)
	}
	encoded, err := json.Marshal(decodedMyFavoritesCursor{
		Version:     1,
		OwnerScope:  myFavoritesOwnerScope(filter.TenantID, filter.PrincipalID),
		AsOf:        filter.At.UTC(),
		FavoritedAt: item.FavoritedAt.UTC(),
		SeriesID:    item.SeriesID,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode", ErrInvalidMyFavoritesCursor)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeMyFavoritesCursor(value string) (decodedMyFavoritesCursor, error) {
	if len(value) > 2048 {
		return decodedMyFavoritesCursor{}, fmt.Errorf(
			"%w: payload is too large",
			ErrInvalidMyFavoritesCursor,
		)
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(encoded) != value {
		return decodedMyFavoritesCursor{}, fmt.Errorf(
			"%w: malformed base64",
			ErrInvalidMyFavoritesCursor,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var cursor decodedMyFavoritesCursor
	if err := decoder.Decode(&cursor); err != nil {
		return decodedMyFavoritesCursor{}, fmt.Errorf(
			"%w: malformed payload",
			ErrInvalidMyFavoritesCursor,
		)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return decodedMyFavoritesCursor{}, fmt.Errorf(
			"%w: trailing payload",
			ErrInvalidMyFavoritesCursor,
		)
	}
	if cursor.Version != 1 || cursor.OwnerScope == "" ||
		cursor.AsOf.IsZero() || cursor.FavoritedAt.IsZero() ||
		cursor.SeriesID == uuid.Nil ||
		cursor.FavoritedAt.After(cursor.AsOf) {
		return decodedMyFavoritesCursor{}, fmt.Errorf(
			"%w: invalid fields",
			ErrInvalidMyFavoritesCursor,
		)
	}
	cursor.AsOf = cursor.AsOf.UTC()
	cursor.FavoritedAt = cursor.FavoritedAt.UTC()
	return cursor, nil
}

func myFavoritesOwnerScope(tenantID uuid.UUID, principalID uuid.UUID) string {
	value := make([]byte, 0, 2*len(tenantID))
	value = append(value, tenantID[:]...)
	value = append(value, principalID[:]...)
	digest := sha256.Sum256(value)
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
