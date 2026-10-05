package standalonepg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const reviewCleanupMaxRetries = 5

var (
	ErrInvalidReviewCleanup            = errors.New("invalid review File cleanup")
	ErrReviewCleanupLeaseLost          = errors.New("review File cleanup lease lost")
	ErrReviewCleanupGenerationInactive = errors.New("review File cleanup generation inactive")
)

// ReviewCleanupClaim is the narrow provider deletion permit returned after a
// File row has been changed to deleting under a PostgreSQL row lock. It carries
// no object key; the local provider derives the one allowed path from FileID.
type ReviewCleanupClaim struct {
	FileID     uuid.UUID
	Lease      time.Time
	RetryCount int
}

// ReviewCleanupRepository owns the Storage File lifecycle SQL for the isolated
// Xiangwan database. Every claim is constrained by the exact tenant upload
// intent and the server-derived object key. Pin locks the same files row, so a
// committed business binding wins before deletion or the cleanup claim wins.
type ReviewCleanupRepository struct {
	db           *sql.DB
	tenantID     uuid.UUID
	generationID uuid.UUID
}

func NewReviewCleanupRepository(
	db *sql.DB, tenantID, generationID uuid.UUID,
) (*ReviewCleanupRepository, error) {
	if db == nil || tenantID == uuid.Nil || generationID == uuid.Nil {
		return nil, ErrInvalidReviewCleanup
	}
	return &ReviewCleanupRepository{
		db: db, tenantID: tenantID, generationID: generationID,
	}, nil
}

// ClaimNext claims an expired pending/confirmed File or renews a deleting row
// whose worker lease is older than ten minutes. The active generation is
// share-locked first, then the File row is claimed in one short transaction;
// no provider IO occurs while either database row is locked.
func (repo *ReviewCleanupRepository) ClaimNext(ctx context.Context) (ReviewCleanupClaim, bool, error) {
	if repo == nil || repo.db == nil || repo.tenantID == uuid.Nil ||
		repo.generationID == uuid.Nil || ctx == nil {
		return ReviewCleanupClaim{}, false, ErrInvalidReviewCleanup
	}
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		return ReviewCleanupClaim{}, false, fmt.Errorf("begin review File cleanup claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var activeGeneration uuid.UUID
	err = tx.QueryRowContext(ctx, `
SELECT generation.active_generation_id
FROM xiangwan_runtime_generations AS generation
JOIN tenants AS tenant ON tenant.id = generation.tenant_id
WHERE generation.singleton_id = 1
  AND generation.scope_key = 'wq-xiangwan'
  AND generation.tenant_id = $1
  AND generation.write_epoch > 0
  AND generation.bootstrap_completed_at IS NOT NULL
  AND tenant.type = 'business'
  AND tenant.metadata @> '{"product_code":"wq-xiangwan"}'::jsonb
FOR SHARE OF generation
`, repo.tenantID).Scan(&activeGeneration)
	if errors.Is(err, sql.ErrNoRows) || err == nil && activeGeneration != repo.generationID {
		return ReviewCleanupClaim{}, false, ErrReviewCleanupGenerationInactive
	}
	if err != nil {
		return ReviewCleanupClaim{}, false, fmt.Errorf("lock review File cleanup generation: %w", err)
	}
	var claim ReviewCleanupClaim
	err = tx.QueryRowContext(ctx, `
WITH candidate AS (
    SELECT file.id
    FROM files AS file
    JOIN xiangwan_review_media_uploads AS intent
      ON intent.file_id = file.id AND intent.tenant_id = $1
    WHERE file.file_key = 'xiangwan-review/' || file.id::text
      AND file.retry_count < $2
      AND (
          (file.delete_after + file.retry_count * INTERVAL '1 hour' < clock_timestamp()
           AND file.status IN ('pending', 'confirmed', 'active'))
          OR
          (file.status = 'deleting'
           AND (file.deleting_at IS NULL
                OR file.deleting_at < clock_timestamp() - INTERVAL '10 minutes'))
      )
    ORDER BY COALESCE(file.delete_after, file.deleting_at) ASC NULLS LAST,
             file.id ASC
    LIMIT 1
    FOR UPDATE OF file SKIP LOCKED
)
UPDATE files AS file
SET status = 'deleting', deleting_at = clock_timestamp()
FROM candidate
WHERE file.id = candidate.id
RETURNING file.id, file.deleting_at, file.retry_count
`, repo.tenantID, reviewCleanupMaxRetries).Scan(
		&claim.FileID, &claim.Lease, &claim.RetryCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewCleanupClaim{}, false, nil
	}
	if err != nil {
		return ReviewCleanupClaim{}, false, fmt.Errorf("claim review File cleanup: %w", err)
	}
	if ReviewObjectKey(claim.FileID) == "" || claim.Lease.IsZero() ||
		claim.RetryCount < 0 || claim.RetryCount >= reviewCleanupMaxRetries {
		return ReviewCleanupClaim{}, false, ErrInvalidReviewCleanup
	}
	if err := tx.Commit(); err != nil {
		return ReviewCleanupClaim{}, false, fmt.Errorf("commit review File cleanup claim: %w", err)
	}
	return claim, true, nil
}

// MarkExpired completes a successful or idempotently replayed provider delete.
// The exact lease prevents a delayed worker from overwriting a newer claim.
func (repo *ReviewCleanupRepository) MarkExpired(
	ctx context.Context, claim ReviewCleanupClaim,
) error {
	if !repo.validClaim(ctx, claim) {
		return ErrInvalidReviewCleanup
	}
	result, err := repo.db.ExecContext(ctx, `
UPDATE files AS file
SET status = 'expired', expired_at = clock_timestamp(),
    deleting_at = NULL, last_error = ''
WHERE file.id = $1 AND file.status = 'deleting'
  AND file.deleting_at = $2
  AND file.file_key = 'xiangwan-review/' || file.id::text
  AND EXISTS (
      SELECT 1 FROM xiangwan_review_media_uploads AS intent
      WHERE intent.file_id = file.id AND intent.tenant_id = $3
  )
`, claim.FileID, claim.Lease, repo.tenantID)
	return reviewCleanupResult(result, err)
}

// RecordFailure restores an eligible File for bounded retry, then leaves it in
// cleanup_failed after the fifth failed provider delete. The caller passes only
// a fixed, non-sensitive error category, never a filesystem path or URL.
func (repo *ReviewCleanupRepository) RecordFailure(
	ctx context.Context, claim ReviewCleanupClaim, category string,
) error {
	if !repo.validClaim(ctx, claim) || category != "provider_delete_failed" {
		return ErrInvalidReviewCleanup
	}
	result, err := repo.db.ExecContext(ctx, `
UPDATE files AS file
SET status = CASE WHEN file.retry_count + 1 >= $4
                  THEN 'cleanup_failed' ELSE 'active' END,
    retry_count = file.retry_count + 1,
    deleting_at = NULL, last_error = $5
WHERE file.id = $1 AND file.status = 'deleting'
  AND file.deleting_at = $2
  AND file.file_key = 'xiangwan-review/' || file.id::text
  AND EXISTS (
      SELECT 1 FROM xiangwan_review_media_uploads AS intent
      WHERE intent.file_id = file.id AND intent.tenant_id = $3
  )
`, claim.FileID, claim.Lease, repo.tenantID,
		reviewCleanupMaxRetries, category)
	return reviewCleanupResult(result, err)
}

func (repo *ReviewCleanupRepository) validClaim(
	ctx context.Context, claim ReviewCleanupClaim,
) bool {
	return repo != nil && repo.db != nil && repo.tenantID != uuid.Nil &&
		repo.generationID != uuid.Nil &&
		ctx != nil && ReviewObjectKey(claim.FileID) != "" &&
		!claim.Lease.IsZero() && claim.RetryCount >= 0 &&
		claim.RetryCount < reviewCleanupMaxRetries
}

func reviewCleanupResult(result sql.Result, err error) error {
	if err != nil {
		return fmt.Errorf("finish review File cleanup: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read review File cleanup result: %w", err)
	}
	if count != 1 {
		return ErrReviewCleanupLeaseLost
	}
	return nil
}
