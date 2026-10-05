package activitypostgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// MaxPublicParticipantAvatars is the per-Session cap for the avatar stack on
// a home card. The SQL below applies activity.MaxPublicParticipantAvatars; the
// literal stays inline so the window bound is visible next to the query.

// ListPublicParticipantAvatars batch-loads up to MaxPublicParticipantAvatars
// avatar URLs per Session for the given Session identities in one query. A
// confirmed Registration only contributes its Principal's avatar when the
// Principal is active, has a non-empty avatar, and has published a Xiangwan
// ConsumerProfile with at least one public field — consumers without a
// published profile never appear on the anonymous home page. Rows are ordered
// by confirmation time so the earliest confirmed participants stack first.
func (repository *Repository) ListPublicParticipantAvatars(
	ctx context.Context,
	tenantID uuid.UUID,
	sessionIDs []uuid.UUID,
) (map[uuid.UUID][]string, error) {
	avatars := make(map[uuid.UUID][]string, len(sessionIDs))
	if repository == nil || repository.db == nil || ctx == nil ||
		tenantID == uuid.Nil || len(sessionIDs) == 0 {
		return avatars, nil
	}
	rows, err := repository.db.queryContext(ctx, `
SELECT ranked.session_id, ranked.avatar_url
FROM (
    SELECT
        registration.session_id,
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
            PARTITION BY registration.session_id
            ORDER BY registration.confirmed_at ASC, registration.id ASC
        ) AS avatar_rank
    FROM xiangwan_registrations AS registration
    JOIN principals AS principal
      ON principal.id = registration.principal_id
    JOIN xiangwan_consumer_profiles AS consumer_profile
      ON consumer_profile.tenant_id = registration.tenant_id
     AND consumer_profile.principal_id = registration.principal_id
    WHERE registration.tenant_id = $1
      AND registration.session_id = ANY($2::UUID[])
      AND registration.participation_status = 'confirmed'
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
ORDER BY ranked.session_id ASC, ranked.avatar_rank ASC
`, tenantID, sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("list xiangwan public participant avatars: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sessionID uuid.UUID
		var avatarURL string
		if scanErr := rows.Scan(&sessionID, &avatarURL); scanErr != nil {
			return nil, fmt.Errorf("scan xiangwan public participant avatar: %w", scanErr)
		}
		avatars[sessionID] = append(avatars[sessionID], avatarURL)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate xiangwan public participant avatars: %w", err)
	}
	return avatars, nil
}
