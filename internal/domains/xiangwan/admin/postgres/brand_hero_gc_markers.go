package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"regexp"
	"sort"
	"strings"
	"time"
)

var brandHeroFilenamePattern = regexp.MustCompile(`^[0-9a-f]{32}\.(?:jpg|png|webp)$`)
var brandHeroFilenameSearchPattern = regexp.MustCompile(`[0-9a-f]{32}\.(?:jpg|png|webp)`)

func brandHeroFilenamesFromReferences(values ...string) []string {
	seen := make(map[string]struct{})
	filenames := make([]string, 0, len(values))
	for _, value := range values {
		for _, filename := range brandHeroFilenameSearchPattern.FindAllString(strings.TrimSpace(value), -1) {
			if _, ok := seen[filename]; ok {
				continue
			}
			seen[filename] = struct{}{}
			filenames = append(filenames, filename)
		}
	}
	sort.Strings(filenames)
	return filenames
}

type BrandHeroGCMarkerStore struct{}

func NewBrandHeroGCMarkerStore() *BrandHeroGCMarkerStore {
	return &BrandHeroGCMarkerStore{}
}

func lockBrandHeroFilename(ctx context.Context, tx *sql.Tx, filename string) error {
	if ctx == nil || tx == nil || !brandHeroFilenamePattern.MatchString(filename) {
		return ErrInvalidCoverGCMarkerQuery
	}
	if _, err := tx.ExecContext(ctx, `
SELECT pg_advisory_xact_lock(hashtextextended($1, 0))
`, filename); err != nil {
		return fmt.Errorf("lock xiangwan brand hero GC filename: %w", err)
	}
	return nil
}

func (store *BrandHeroGCMarkerStore) OrphanedAt(
	ctx context.Context, database RowQueryer, filename string,
) (time.Time, bool, error) {
	if store == nil || ctx == nil || database == nil ||
		!brandHeroFilenamePattern.MatchString(filename) {
		return time.Time{}, false, ErrInvalidCoverGCMarkerQuery
	}
	var orphanedAt time.Time
	err := database.QueryRowContext(ctx, `
SELECT orphaned_at FROM xiangwan_brand_hero_gc_markers WHERE filename = $1
`, filename).Scan(&orphanedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read xiangwan brand hero GC marker: %w", err)
	}
	return orphanedAt.UTC(), true, nil
}

func (store *BrandHeroGCMarkerStore) IsDeleted(
	ctx context.Context, database RowQueryer, filename string,
) (bool, error) {
	if store == nil || ctx == nil || database == nil ||
		!brandHeroFilenamePattern.MatchString(filename) {
		return false, ErrInvalidCoverGCMarkerQuery
	}
	var deleted bool
	err := database.QueryRowContext(ctx, `
SELECT deleted_at IS NOT NULL
FROM xiangwan_brand_hero_gc_markers
WHERE filename = $1
`, filename).Scan(&deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read xiangwan brand hero deletion tombstone: %w", err)
	}
	return deleted, nil
}

func (store *BrandHeroGCMarkerStore) MarkOrphaned(
	ctx context.Context, database RowExecer, filename string,
) error {
	if store == nil || ctx == nil || database == nil ||
		!brandHeroFilenamePattern.MatchString(filename) {
		return ErrInvalidCoverGCMarkerQuery
	}
	_, err := database.ExecContext(ctx, `
INSERT INTO xiangwan_brand_hero_gc_markers (filename, orphaned_at)
VALUES ($1, NOW()) ON CONFLICT (filename) DO NOTHING
`, filename)
	if err != nil {
		return fmt.Errorf("mark xiangwan brand hero GC marker: %w", err)
	}
	return nil
}

func (store *BrandHeroGCMarkerStore) Clear(
	ctx context.Context, database RowExecer, filename string,
) error {
	if store == nil || ctx == nil || database == nil ||
		!brandHeroFilenamePattern.MatchString(filename) {
		return ErrInvalidCoverGCMarkerQuery
	}
	_, err := database.ExecContext(ctx, `
DELETE FROM xiangwan_brand_hero_gc_markers WHERE filename = $1
`, filename)
	if err != nil {
		return fmt.Errorf("clear xiangwan brand hero GC marker: %w", err)
	}
	return nil
}

func (store *BrandHeroGCMarkerStore) MarkDeleted(
	ctx context.Context, database RowExecer, filename string,
) error {
	if store == nil || ctx == nil || database == nil ||
		!brandHeroFilenamePattern.MatchString(filename) {
		return ErrInvalidCoverGCMarkerQuery
	}
	_, err := database.ExecContext(ctx, `
INSERT INTO xiangwan_brand_hero_gc_markers (filename, orphaned_at, deleted_at)
VALUES ($1, NOW(), NOW())
ON CONFLICT (filename) DO UPDATE
SET deleted_at = COALESCE(xiangwan_brand_hero_gc_markers.deleted_at, EXCLUDED.deleted_at)
`, filename)
	if err != nil {
		return fmt.Errorf("mark xiangwan deleted brand hero: %w", err)
	}
	return nil
}

type BrandHeroReferenceQuerier struct{ tenantID uuid.UUID }

func NewBrandHeroReferenceQuerier(tenantID uuid.UUID) (*BrandHeroReferenceQuerier, error) {
	if tenantID == uuid.Nil {
		return nil, ErrInvalidCoverGCMarkerQuery
	}
	return &BrandHeroReferenceQuerier{tenantID: tenantID}, nil
}

func (querier *BrandHeroReferenceQuerier) Referenced(
	ctx context.Context, database RowQueryer, filename string,
) (bool, error) {
	if querier == nil || ctx == nil || database == nil ||
		!brandHeroFilenamePattern.MatchString(filename) {
		return false, ErrInvalidCoverGCMarkerQuery
	}
	var referenced bool
	err := database.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM xiangwan_brand_profile_publications
    JOIN xiangwan_brand_profiles AS profile
      ON profile.tenant_id = xiangwan_brand_profile_publications.tenant_id
     AND profile.current_publication_version = xiangwan_brand_profile_publications.publication_version
    WHERE xiangwan_brand_profile_publications.tenant_id = $1
      AND position($2 in hero_image_url) > 0
)
`, querier.tenantID, filename).Scan(&referenced)
	if err != nil {
		return false, fmt.Errorf("probe xiangwan brand hero reference: %w", err)
	}
	return referenced, nil
}
