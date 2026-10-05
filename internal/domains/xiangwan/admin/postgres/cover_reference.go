package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
)

// coverImageFilenamePattern pins the only filenames the orphan GC ever asks
// about: the server-generated public cover shape. It is the SQL boundary's
// own guard so the LIKE probe below can never be pointed at an arbitrary
// string even if a future caller forgets to validate.
var coverImageFilenamePattern = regexp.MustCompile(`^[0-9a-f]{32}\.(jpg|png|webp)$`)

// ErrInvalidCoverReferenceQuery marks a cover reference probe that can never
// be answered (nil queryer, empty or non-canonical filename).
var ErrInvalidCoverReferenceQuery = errors.New(
	"invalid xiangwan cover reference query",
)

// CoverImageReferenceQuerier answers whether one stored cover filename is
// still referenced by any activity Instance, either as its cover_image_url
// or inside its detail_blocks JSONB text. It backs the upload-time orphan GC.
type CoverImageReferenceQuerier struct {
	tenantID uuid.UUID
}

func NewCoverImageReferenceQuerier(tenantID uuid.UUID) (*CoverImageReferenceQuerier, error) {
	if tenantID == uuid.Nil {
		return nil, ErrInvalidCoverReferenceQuery
	}
	return &CoverImageReferenceQuerier{tenantID: tenantID}, nil
}

// Referenced runs one lightweight EXISTS probe with a LIKE on both reference
// columns. The filename is bound as a parameter, so the wildcard concat
// stays inside the predicate and never opens string interpolation.
func (querier *CoverImageReferenceQuerier) Referenced(
	ctx context.Context,
	queryer RowQueryer,
	filename string,
) (bool, error) {
	if querier == nil || querier.tenantID == uuid.Nil || ctx == nil ||
		queryer == nil || !coverImageFilenamePattern.MatchString(filename) {
		return false, ErrInvalidCoverReferenceQuery
	}
	var referenced bool
	err := queryer.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM xiangwan_activity_instances
    WHERE tenant_id = $1
      AND (
          cover_image_url LIKE '%' || $2 || '%'
          OR detail_blocks::TEXT LIKE '%' || $2 || '%'
      )
)
`, querier.tenantID, filename).Scan(&referenced)
	if err != nil {
		return false, fmt.Errorf("probe xiangwan cover reference: %w", err)
	}
	return referenced, nil
}
