package standalonepg

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ProductCode is stamped by Writer into Content metadata. It is a constant of
// this provider seam rather than a caller-controlled input.
const ProductCode = "wq-xiangwan"

// SQLDB is the standard database/sql surface needed by Reader. Both *sql.DB
// and *sql.Tx satisfy it, so a caller can hydrate a review inside the same
// transaction that created or published its product relation.
type SQLDB interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Reader is a Redis-free Content read seam. It applies the fixed Xiangwan
// review predicate, tenant/owner scope, private visibility, and optional exact
// revision in SQL before returning any block data.
type Reader struct {
	db reviewQueryExecutor
}

// NewReader constructs a reader over a *sql.DB or *sql.Tx.
func NewReader(db SQLDB) (*Reader, error) {
	if db == nil {
		return nil, ErrInvalidReader
	}
	return &Reader{db: sqlReviewQueryExecutor{db: db}}, nil
}

// ReadReview reads one eligible private review and its ordered blocks. A nil
// RevisionAt permits a draft read; publication/resource hydration should pass
// the exact revision pinned by its immutable relation snapshot.
func (reader *Reader) ReadReview(
	ctx context.Context,
	in ReviewReadInput,
) (ReviewDocument, error) {
	if !reader.valid(ctx) || !validReadInput(in) {
		return ReviewDocument{}, ErrInvalidReader
	}
	query := reviewSelect
	args := []any{in.ContentID, in.TenantID, in.PrincipalID}
	if in.RevisionAt != nil {
		query += "\n  AND content.updated_at = $4"
		args = append(args, normalizeTimestamp(*in.RevisionAt))
	}
	query += "\nORDER BY content_block.sort_order, content_block.id"
	rows, err := reader.db.queryContext(ctx, query, args...)
	if err != nil {
		return ReviewDocument{}, fmt.Errorf("read Content review: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var document ReviewDocument
	found := false
	seenBlockIDs := make(map[uuid.UUID]struct{})
	seenSortOrders := make(map[int]struct{})
	for rows.Next() {
		var (
			blockID        uuid.NullUUID
			blockType      sql.NullString
			blockSortOrder sql.NullInt64
			blockData      []byte
			blockCreatedAt sql.NullTime
			blockUpdatedAt sql.NullTime
		)
		if err := rows.Scan(
			&document.ID,
			&document.TenantID,
			&document.PrincipalID,
			&document.Title,
			&document.Status,
			&document.Visibility,
			&document.OwnerType,
			&document.CreatedAt,
			&document.UpdatedAt,
			&blockID,
			&blockType,
			&blockSortOrder,
			&blockData,
			&blockCreatedAt,
			&blockUpdatedAt,
		); err != nil {
			return ReviewDocument{}, fmt.Errorf("scan Content review: %w", err)
		}
		found = true
		if !documentFactsValid(document) {
			return ReviewDocument{}, ErrReviewFactsConflict
		}
		if !blockID.Valid {
			// LEFT JOIN permits a draft with no blocks. A row with a partially
			// null block is corrupt and must never be projected.
			if blockType.Valid || blockSortOrder.Valid || blockCreatedAt.Valid || blockUpdatedAt.Valid || len(blockData) > 0 {
				return ReviewDocument{}, ErrReviewFactsConflict
			}
			continue
		}
		if !blockType.Valid || !blockSortOrder.Valid || !blockCreatedAt.Valid ||
			!blockUpdatedAt.Valid || len(blockData) == 0 || !json.Valid(blockData) {
			return ReviewDocument{}, ErrReviewFactsConflict
		}
		block := ReviewBlock{
			ID: blockID.UUID, Type: blockType.String,
			SortOrder: int(blockSortOrder.Int64),
			Data:      append(json.RawMessage(nil), blockData...),
			CreatedAt: blockCreatedAt.Time.UTC(), UpdatedAt: blockUpdatedAt.Time.UTC(),
		}
		if block.SortOrder < 0 || !isReviewBlockType(block.Type) ||
			block.Type != canonicalBlockType(block.Type) || !jsonObject(block.Data) ||
			block.UpdatedAt.Before(block.CreatedAt) {
			return ReviewDocument{}, ErrReviewFactsConflict
		}
		if _, exists := seenBlockIDs[block.ID]; exists {
			return ReviewDocument{}, ErrReviewFactsConflict
		}
		if _, exists := seenSortOrders[block.SortOrder]; exists {
			return ReviewDocument{}, ErrReviewFactsConflict
		}
		seenBlockIDs[block.ID] = struct{}{}
		seenSortOrders[block.SortOrder] = struct{}{}
		document.Blocks = append(document.Blocks, block)
	}
	if err := rows.Err(); err != nil {
		return ReviewDocument{}, fmt.Errorf("iterate Content review: %w", err)
	}
	if !found {
		return ReviewDocument{}, ErrReviewNotFound
	}
	if !documentFactsValid(document) {
		return ReviewDocument{}, ErrReviewFactsConflict
	}
	return document, nil
}

// ReadReviewAt is a convenience wrapper for callers with an immutable
// relation's exact Content revision.
func (reader *Reader) ReadReviewAt(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	contentID uuid.UUID,
	revisionAt time.Time,
) (ReviewDocument, error) {
	return reader.ReadReview(ctx, ReviewReadInput{
		TenantID: tenantID, PrincipalID: principalID, ContentID: contentID,
		RevisionAt: &revisionAt,
	})
}

func (reader *Reader) valid(ctx context.Context) bool {
	return reader != nil && reader.db != nil && ctx != nil
}

func validReadInput(in ReviewReadInput) bool {
	if in.TenantID == uuid.Nil || in.PrincipalID == uuid.Nil || in.ContentID == uuid.Nil {
		return false
	}
	return in.RevisionAt == nil || !in.RevisionAt.IsZero()
}

func documentFactsValid(document ReviewDocument) bool {
	return document.ID != uuid.Nil && document.TenantID != uuid.Nil &&
		document.PrincipalID != uuid.Nil && document.Status != "" &&
		(document.Status == statusActive || document.Status == statusReviewing) &&
		document.Visibility == visibilityPrivate && document.OwnerType == ownerTypeUser &&
		!document.CreatedAt.IsZero() && !document.UpdatedAt.IsZero() &&
		!document.UpdatedAt.Before(document.CreatedAt)
}

func jsonObject(data []byte) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(data, &object) == nil && object != nil
}

const reviewSelect = `
SELECT
    content.id,
    content.tenant_id,
    content.principal_id,
    COALESCE(content.title, ''),
    content.status,
    content.visibility,
    content.owner_type,
    content.created_at,
    content.updated_at,
    content_block.id,
    content_block.type,
    content_block.sort_order,
    content_block.data,
    content_block.created_at,
    content_block.updated_at
FROM contents AS content
LEFT JOIN blocks AS content_block
  ON content_block.content_id = content.id
WHERE content.id = $1
  AND content.tenant_id = $2
  AND content.principal_id = $3
  AND content.type = 'review'
  AND content.status IN ('active', 'reviewing')
  AND content.visibility = 'private'
  AND content.owner_type = 'user'
  AND content.metadata->>'product_code' = 'wq-xiangwan'
  AND content.deleted_at IS NULL
`

type reviewQueryExecutor interface {
	queryContext(context.Context, string, ...any) (reviewRows, error)
}

type sqlReviewQueryExecutor struct {
	db SQLDB
}

func (executor sqlReviewQueryExecutor) queryContext(
	ctx context.Context,
	query string,
	args ...any,
) (reviewRows, error) {
	rows, err := executor.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return sqlReviewRows{rows: rows}, nil
}

type reviewRows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

type sqlReviewRows struct {
	rows *sql.Rows
}

func (rows sqlReviewRows) Next() bool { return rows.rows.Next() }
func (rows sqlReviewRows) Scan(destinations ...any) error {
	return rows.rows.Scan(destinations...)
}
func (rows sqlReviewRows) Err() error   { return rows.rows.Err() }
func (rows sqlReviewRows) Close() error { return rows.rows.Close() }
