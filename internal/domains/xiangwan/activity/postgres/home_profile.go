package activitypostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

// ReadPublicHomeProfile resolves only the approved snapshot selected by the
// tenant BrandProfile. Draft fields are intentionally absent from the query.
func (repository *Repository) ReadPublicHomeProfile(
	ctx context.Context,
	tenantID uuid.UUID,
) (activity.PublicHomeProfile, error) {
	var profile activity.PublicHomeProfile
	var quickTagsJSON []byte
	err := repository.db.queryRowContext(ctx, `
SELECT
    brand_profile.lifecycle_status,
    brand_profile.current_publication_version,
    publication.community_name,
    publication.brand_intro,
    publication.hero_mode,
    publication.hero_eyebrow,
    publication.hero_subtitle,
    publication.hero_image_url,
    publication.hero_image_alt,
    publication.quick_tags,
    publication.published_at
FROM xiangwan_brand_profiles AS brand_profile
JOIN xiangwan_brand_profile_publications AS publication
  ON publication.tenant_id = brand_profile.tenant_id
 AND publication.publication_version = brand_profile.current_publication_version
WHERE brand_profile.tenant_id = $1
  AND brand_profile.lifecycle_status IN ('active', 'suspended')
`, tenantID).Scan(
		&profile.LifecycleStatus,
		&profile.PublicationVersion,
		&profile.CommunityName,
		&profile.BrandIntro,
		&profile.HeroMode,
		&profile.HeroEyebrow,
		&profile.HeroSubtitle,
		&profile.HeroImageURL,
		&profile.HeroImageAlt,
		&quickTagsJSON,
		&profile.PublishedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return activity.PublicHomeProfile{}, activity.ErrPublicHomeProfileUnavailable
	}
	if err != nil {
		return activity.PublicHomeProfile{}, fmt.Errorf(
			"read xiangwan public BrandProfile: %w",
			err,
		)
	}
	if err := json.Unmarshal(quickTagsJSON, &profile.AvailableQuickTags); err != nil {
		return activity.PublicHomeProfile{}, fmt.Errorf(
			"decode xiangwan public BrandProfile quick tags: %w",
			err,
		)
	}
	if profile.AvailableQuickTags == nil {
		profile.AvailableQuickTags = []activity.HomeQuickTag{}
	}
	if err := activity.ValidatePublicHomeProfile(profile); err != nil {
		return activity.PublicHomeProfile{}, err
	}
	return profile, nil
}
