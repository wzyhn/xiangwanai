// Package standalonepg exposes the narrow PostgreSQL Content seam used by an
// independently deployed product runtime.
//
// The package intentionally has no dependency on the Content HTTP service,
// Gin, middleware, cache, event bus, or Redis. Writer instances are bound to a
// caller-owned *sql.Tx so a product can commit its generation fence, Content
// row, product relation, audit row, and outbox in one transaction.
package standalonepg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	contentTypeReview = "review"
	ownerTypeUser     = "user"
	visibilityPrivate = "private"
	statusActive      = "active"
	statusReviewing   = "reviewing"
)

var (
	// ErrInvalidWriter indicates a malformed writer, context, or command.
	ErrInvalidWriter = errors.New("invalid Content standalone PostgreSQL writer")
	// ErrInvalidReader indicates a malformed reader, context, or query.
	ErrInvalidReader = errors.New("invalid Content standalone PostgreSQL reader")
	// ErrReviewNotFound is returned for a missing, deleted, non-private, or
	// otherwise ineligible review. Callers should not distinguish those cases.
	ErrReviewNotFound = errors.New("Content review not found")
	// ErrReviewFactsConflict indicates that persisted Content/Block facts do not
	// satisfy this seam's review contract.
	ErrReviewFactsConflict = errors.New("Content review facts conflict")
	// ErrReviewWriteConflict indicates an optimistic revision or row-count
	// conflict. The caller may re-read and present a fresh edit operation.
	ErrReviewWriteConflict = errors.New("Content review write conflict")
)

// ReviewBlockInput is a server-validated block supplied to CreateReview or
// UpdateReview. The ID is caller supplied so a retry can reuse deterministic
// identities inside the caller's transaction; the writer never starts a
// second transaction for block rows.
type ReviewBlockInput struct {
	ID        uuid.UUID
	Type      string
	SortOrder int
	Data      json.RawMessage
}

// CreateReviewInput creates one private Content review candidate and its
// blocks. Status may be active or reviewing; an empty status selects active.
// CreatedAt is used for both created_at and updated_at, making the first
// revision explicit to the caller and to a later Xiangwan resource binding.
type CreateReviewInput struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	PrincipalID uuid.UUID
	Title       string
	Status      string
	CreatedAt   time.Time
	Blocks      []ReviewBlockInput
}

// UpdateReviewInput replaces the title, status, and complete ordered block set
// of an existing unbound review. ExpectedRevision must equal the current
// contents.updated_at. UpdatedAt must be strictly later than that revision.
type UpdateReviewInput struct {
	TenantID         uuid.UUID
	PrincipalID      uuid.UUID
	ContentID        uuid.UUID
	ExpectedRevision time.Time
	UpdatedAt        time.Time
	Title            string
	Status           string
	Blocks           []ReviewBlockInput
}

// ReviewDocument is the provider-owned projection returned by write methods.
// It contains only Content and Block facts; product relation/publication facts
// remain owned by the product domain.
type ReviewDocument struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	PrincipalID uuid.UUID
	Title       string
	Status      string
	Visibility  string
	OwnerType   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Blocks      []ReviewBlock
}

// ReviewBlock is an ordered Content block. Data is copied from the database so
// callers cannot mutate a provider-owned buffer after a read.
type ReviewBlock struct {
	ID        uuid.UUID
	Type      string
	SortOrder int
	Data      json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ReviewReadInput selects one exact private review revision. RevisionAt is
// optional for internal drafts; publication/resource readers should always set
// it to the revision pinned by their relation snapshot.
type ReviewReadInput struct {
	TenantID    uuid.UUID
	PrincipalID uuid.UUID
	ContentID   uuid.UUID
	RevisionAt  *time.Time
}

// Writer is bound to a caller-owned PostgreSQL transaction. It never begins,
// commits, or rolls back that transaction.
type Writer struct {
	tx reviewTransaction
}

// NewWriter creates a transaction-bound Content writer.
func NewWriter(tx *sql.Tx) (*Writer, error) {
	if tx == nil {
		return nil, ErrInvalidWriter
	}
	return &Writer{tx: tx}, nil
}

// CreateReview inserts a private review Content row and all supplied blocks.
// The caller must keep the transaction open and commit any product relation or
// outbox facts in the same unit of work.
func (writer *Writer) CreateReview(
	ctx context.Context,
	in CreateReviewInput,
) (ReviewDocument, error) {
	if !writer.valid(ctx) {
		return ReviewDocument{}, ErrInvalidWriter
	}
	status, err := normalizeStatus(in.Status)
	if err != nil {
		return ReviewDocument{}, err
	}
	if err := validateCreateInput(in); err != nil {
		return ReviewDocument{}, err
	}
	in.CreatedAt = normalizeTimestamp(in.CreatedAt)
	in.Status = status
	if _, err := writer.tx.ExecContext(ctx, `
INSERT INTO contents (
    id, principal_id, space_id, type, title, status, visibility,
    owner_type, metadata, tenant_id, created_at, updated_at
) VALUES ($1, $2, NULL, 'review', $3, $4, 'private',
          'user', jsonb_build_object('product_code', 'wq-xiangwan'), $5, $6, $6)
`, in.ID, in.PrincipalID, in.Title, in.Status, in.TenantID, in.CreatedAt); err != nil {
		return ReviewDocument{}, fmt.Errorf("create Content review: %w", err)
	}
	if err := insertBlocks(ctx, writer.tx, in.ID, in.Blocks, in.CreatedAt); err != nil {
		return ReviewDocument{}, err
	}
	return ReviewDocument{
		ID:          in.ID,
		TenantID:    in.TenantID,
		PrincipalID: in.PrincipalID,
		Title:       in.Title,
		Status:      in.Status,
		Visibility:  visibilityPrivate,
		OwnerType:   ownerTypeUser,
		CreatedAt:   in.CreatedAt,
		UpdatedAt:   in.CreatedAt,
		Blocks:      copyInputBlocks(in.Blocks, in.CreatedAt),
	}, nil
}

// UpdateReview replaces an unbound private review's complete body under a row
// lock. Database migration 765 rejects this statement after a Xiangwan
// resource relation binds the Content revision, preserving the snapshot
// immutability boundary even if a caller bypasses this optimistic check.
func (writer *Writer) UpdateReview(
	ctx context.Context,
	in UpdateReviewInput,
) (ReviewDocument, error) {
	if !writer.valid(ctx) {
		return ReviewDocument{}, ErrInvalidWriter
	}
	status, err := normalizeStatus(in.Status)
	if err != nil {
		return ReviewDocument{}, err
	}
	if err := validateUpdateInput(in); err != nil {
		return ReviewDocument{}, err
	}
	in.ExpectedRevision = normalizeTimestamp(in.ExpectedRevision)
	in.UpdatedAt = normalizeTimestamp(in.UpdatedAt)
	in.Status = status
	var createdAt, currentRevision time.Time
	err = writer.tx.QueryRowContext(ctx, `
SELECT created_at, updated_at
FROM contents
WHERE id = $1
  AND tenant_id = $2
  AND principal_id = $3
  AND type = 'review'
  AND visibility = 'private'
  AND status IN ('active', 'reviewing')
  AND metadata->>'product_code' = 'wq-xiangwan'
  AND deleted_at IS NULL
FOR UPDATE
`, in.ContentID, in.TenantID, in.PrincipalID).Scan(&createdAt, &currentRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewDocument{}, ErrReviewNotFound
	}
	if err != nil {
		return ReviewDocument{}, fmt.Errorf("lock Content review for update: %w", err)
	}
	if !currentRevision.Equal(in.ExpectedRevision) {
		return ReviewDocument{}, ErrReviewWriteConflict
	}
	if !in.UpdatedAt.After(currentRevision) {
		return ReviewDocument{}, ErrReviewWriteConflict
	}
	result, err := writer.tx.ExecContext(ctx, `
UPDATE contents
SET title = $4, status = $5, updated_at = $6
WHERE id = $1
  AND tenant_id = $2
  AND principal_id = $3
  AND type = 'review'
  AND visibility = 'private'
  AND status IN ('active', 'reviewing')
  AND metadata->>'product_code' = 'wq-xiangwan'
  AND deleted_at IS NULL
`, in.ContentID, in.TenantID, in.PrincipalID, in.Title, in.Status, in.UpdatedAt)
	if err != nil {
		return ReviewDocument{}, fmt.Errorf("update Content review: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return ReviewDocument{}, err
	}
	if _, err := writer.tx.ExecContext(ctx,
		"DELETE FROM blocks WHERE content_id = $1", in.ContentID); err != nil {
		return ReviewDocument{}, fmt.Errorf("replace Content review blocks: %w", err)
	}
	if err := insertBlocks(ctx, writer.tx, in.ContentID, in.Blocks, in.UpdatedAt); err != nil {
		return ReviewDocument{}, err
	}
	return ReviewDocument{
		ID:          in.ContentID,
		TenantID:    in.TenantID,
		PrincipalID: in.PrincipalID,
		Title:       in.Title,
		Status:      in.Status,
		Visibility:  visibilityPrivate,
		OwnerType:   ownerTypeUser,
		CreatedAt:   createdAt.UTC(),
		UpdatedAt:   in.UpdatedAt.UTC(),
		Blocks:      copyInputBlocks(in.Blocks, in.UpdatedAt),
	}, nil
}

func (writer *Writer) valid(ctx context.Context) bool {
	return writer != nil && writer.tx != nil && ctx != nil
}

func validateCreateInput(in CreateReviewInput) error {
	if in.ID == uuid.Nil || in.TenantID == uuid.Nil || in.PrincipalID == uuid.Nil ||
		in.CreatedAt.IsZero() {
		return ErrInvalidWriter
	}
	return validateBlocks(in.Blocks)
}

func validateUpdateInput(in UpdateReviewInput) error {
	if in.TenantID == uuid.Nil || in.PrincipalID == uuid.Nil ||
		in.ContentID == uuid.Nil || in.ExpectedRevision.IsZero() ||
		in.UpdatedAt.IsZero() {
		return ErrInvalidWriter
	}
	return validateBlocks(in.Blocks)
}

func normalizeStatus(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return statusActive, nil
	}
	if value != statusActive && value != statusReviewing {
		return "", ErrInvalidWriter
	}
	return value, nil
}

func validateBlocks(blocks []ReviewBlockInput) error {
	seenOrders := make(map[int]struct{}, len(blocks))
	seenIDs := make(map[uuid.UUID]struct{}, len(blocks))
	for _, block := range blocks {
		if block.ID == uuid.Nil || block.SortOrder < 0 ||
			block.Type != canonicalBlockType(block.Type) ||
			!isReviewBlockType(block.Type) || len(block.Data) == 0 ||
			!json.Valid(block.Data) || string(block.Data) == "null" {
			return ErrInvalidWriter
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(block.Data, &object); err != nil || object == nil {
			return ErrInvalidWriter
		}
		if _, exists := seenOrders[block.SortOrder]; exists {
			return ErrInvalidWriter
		}
		if _, exists := seenIDs[block.ID]; exists {
			return ErrInvalidWriter
		}
		seenOrders[block.SortOrder] = struct{}{}
		seenIDs[block.ID] = struct{}{}
	}
	return nil
}

func canonicalBlockType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isReviewBlockType(value string) bool {
	switch value {
	case "text", "image", "link", "file", "video", "audio":
		return true
	default:
		return false
	}
}

func insertBlocks(
	ctx context.Context,
	executor interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	},
	contentID uuid.UUID,
	blocks []ReviewBlockInput,
	at time.Time,
) error {
	for _, block := range blocks {
		if _, err := executor.ExecContext(ctx, `
INSERT INTO blocks (id, content_id, type, sort_order, data, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5::jsonb, $6, $6)
`, block.ID, contentID, block.Type, block.SortOrder, []byte(block.Data), at); err != nil {
			return fmt.Errorf("write Content review block: %w", err)
		}
	}
	return nil
}

func copyInputBlocks(blocks []ReviewBlockInput, at time.Time) []ReviewBlock {
	at = normalizeTimestamp(at)
	result := make([]ReviewBlock, 0, len(blocks))
	for _, block := range blocks {
		data := append(json.RawMessage(nil), block.Data...)
		result = append(result, ReviewBlock{
			ID: block.ID, Type: block.Type, SortOrder: block.SortOrder,
			Data: data, CreatedAt: at.UTC(), UpdatedAt: at.UTC(),
		})
	}
	return result
}

// PostgreSQL timestamptz stores microseconds. Normalize caller timestamps
// before returning a revision so a relation created later in the same
// transaction pins the exact value that PostgreSQL persisted.
func normalizeTimestamp(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func requireOneRow(result sql.Result) error {
	if result == nil {
		return ErrReviewWriteConflict
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return ErrReviewWriteConflict
	}
	return nil
}

// reviewTransaction keeps the writer's dependency surface to caller-owned
// transaction methods. NewWriter only accepts *sql.Tx; products cannot pass a
// non-transactional *sql.DB through the public constructor.
type reviewTransaction interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
