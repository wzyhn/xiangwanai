package activitypostgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ListPublicFavoriteAvatars batch-loads a bounded public preview of the
// current Series "want to go" relations. A Favorite is a private relation;
// only a Principal's already-published public profile and non-empty avatar
// cross this anonymous boundary. Principal IDs and relation identities never
// enter the result.
func (repository *Repository) ListPublicFavoriteAvatars(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesIDs []uuid.UUID,
) (map[uuid.UUID][]string, error) {
	avatars := make(map[uuid.UUID][]string, len(seriesIDs))
	if repository == nil || repository.db == nil || ctx == nil ||
		tenantID == uuid.Nil || len(seriesIDs) == 0 {
		return avatars, nil
	}
	rows, err := repository.db.queryContext(ctx, `
SELECT ranked.series_id, ranked.avatar_url
FROM (
    SELECT
        favorite.series_id,
        CASE
            WHEN principal.avatar_file_id IS NOT NULL
             AND (
                 principal.avatar_url =
                     '/api/v1/auth/principals/' || principal.id::text ||
                     '/avatar/' || principal.avatar_file_id::text
                 OR principal.avatar_url =
                     '/api/v1/files/' || principal.avatar_file_id::text ||
                     '/content'
                 OR principal.avatar_url LIKE
                     '/api/v1/files/' || principal.avatar_file_id::text ||
                     '/content?%'
             )
            THEN '/api/v1/xiangwan/avatars/legacy/' ||
                 principal.id::text || '/' || principal.avatar_file_id::text
            ELSE principal.avatar_url
        END AS avatar_url,
        ROW_NUMBER() OVER (
            PARTITION BY favorite.series_id
            ORDER BY favorite.created_at DESC, favorite.principal_id DESC
        ) AS avatar_rank
    FROM xiangwan_series_favorites AS favorite
    JOIN principals AS principal
      ON principal.id = favorite.principal_id
    JOIN xiangwan_consumer_profiles AS consumer_profile
      ON consumer_profile.tenant_id = favorite.tenant_id
     AND consumer_profile.principal_id = favorite.principal_id
    WHERE favorite.tenant_id = $1
      AND favorite.series_id = ANY($2::UUID[])
      AND principal.status = 'active'
      AND principal.deleted_at IS NULL
      AND principal.avatar_url <> ''
      AND (
          consumer_profile.occupation_public
          OR consumer_profile.introduction_public
          OR consumer_profile.tags_public
      )
) AS ranked
WHERE ranked.avatar_rank <= 3
ORDER BY ranked.series_id ASC, ranked.avatar_rank ASC
`, tenantID, seriesIDs)
	if err != nil {
		return nil, fmt.Errorf("list xiangwan public favorite avatars: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var seriesID uuid.UUID
		var avatarURL string
		if scanErr := rows.Scan(&seriesID, &avatarURL); scanErr != nil {
			return nil, fmt.Errorf("scan xiangwan public favorite avatar: %w", scanErr)
		}
		avatars[seriesID] = append(avatars[seriesID], avatarURL)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate xiangwan public favorite avatars: %w", err)
	}
	return avatars, nil
}
