package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ErrInvalidCoverGCMarkerQuery marks a cover GC marker call that can never be
// executed (nil database, nil context, or a non-canonical filename).
var ErrInvalidCoverGCMarkerQuery = errors.New(
	"invalid xiangwan cover GC marker query",
)

// RowExecer is the write surface the cover GC marker store needs.
type RowExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// CoverGCMarkerDB is the database surface the cover GC marker store needs.
type CoverGCMarkerDB interface {
	RowQueryer
	RowExecer
}

// CoverGCMarkerStore persists, per stored cover filename, the first moment
// the orphan GC observed the file without any Instance reference. It backs
// the two-phase cover GC in the api package: deletion is deferred until the
// marker ages past the retention window, so a long-referenced cover that was
// just replaced cannot be collected while clients still hold its immutable
// cached URL.
type CoverGCMarkerStore struct{}

// lockCoverFilename serializes a presentation attachment with the collector
// for one immutable object. The lock is transaction-scoped, so callers must
// hold the transaction until their reference update is committed.
func lockCoverFilename(ctx context.Context, tx *sql.Tx, filename string) error {
	if ctx == nil || tx == nil || !coverImageFilenamePattern.MatchString(filename) {
		return ErrInvalidCoverGCMarkerQuery
	}
	if _, err := tx.ExecContext(ctx, `
SELECT pg_advisory_xact_lock(hashtextextended($1, 0))
`, filename); err != nil {
		return fmt.Errorf("lock xiangwan cover GC filename: %w", err)
	}
	return nil
}

var coverFilenamePattern = regexp.MustCompile(`[0-9a-f]{32}\.(?:jpg|png|webp)`)

func NewCoverGCMarkerStore() *CoverGCMarkerStore {
	return &CoverGCMarkerStore{}
}

// OrphanedAt reports when the filename was first observed unreferenced. The
// absent case is (zero, false, nil), never an error.
func (store *CoverGCMarkerStore) OrphanedAt(
	ctx context.Context,
	database RowQueryer,
	filename string,
) (time.Time, bool, error) {
	if store == nil || ctx == nil || database == nil ||
		!coverImageFilenamePattern.MatchString(filename) {
		return time.Time{}, false, ErrInvalidCoverGCMarkerQuery
	}
	var orphanedAt time.Time
	err := database.QueryRowContext(ctx, `
SELECT orphaned_at
FROM xiangwan_cover_gc_markers
WHERE filename = $1
`, filename).Scan(&orphanedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf(
			"read xiangwan cover GC marker: %w", err,
		)
	}
	return orphanedAt.UTC(), true, nil
}

func (store *CoverGCMarkerStore) IsDeleted(
	ctx context.Context,
	database RowQueryer,
	filename string,
) (bool, error) {
	if store == nil || ctx == nil || database == nil ||
		!coverImageFilenamePattern.MatchString(filename) {
		return false, ErrInvalidCoverGCMarkerQuery
	}
	var deleted bool
	err := database.QueryRowContext(ctx, `
SELECT deleted_at IS NOT NULL
FROM xiangwan_cover_gc_markers
WHERE filename = $1
`, filename).Scan(&deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read xiangwan cover deletion tombstone: %w", err)
	}
	return deleted, nil
}

// MarkOrphaned records the first unreferenced observation of one cover file.
// A filename that is already marked keeps its original orphaned_at, so the
// retention window is always counted from the first observation.
func (store *CoverGCMarkerStore) MarkOrphaned(
	ctx context.Context,
	database RowExecer,
	filename string,
) error {
	if store == nil || ctx == nil || database == nil ||
		!coverImageFilenamePattern.MatchString(filename) {
		return ErrInvalidCoverGCMarkerQuery
	}
	_, err := database.ExecContext(ctx, `
INSERT INTO xiangwan_cover_gc_markers (filename, orphaned_at)
VALUES ($1, NOW())
ON CONFLICT (filename) DO NOTHING
`, filename)
	if err != nil {
		return fmt.Errorf("mark xiangwan cover GC marker: %w", err)
	}
	return nil
}

// Clear drops the marker of one cover file after the file was deleted or
// became referenced again. Deleting a missing marker is a no-op.
func (store *CoverGCMarkerStore) Clear(
	ctx context.Context,
	database RowExecer,
	filename string,
) error {
	if store == nil || ctx == nil || database == nil ||
		!coverImageFilenamePattern.MatchString(filename) {
		return ErrInvalidCoverGCMarkerQuery
	}
	_, err := database.ExecContext(ctx, `
DELETE FROM xiangwan_cover_gc_markers
WHERE filename = $1
`, filename)
	if err != nil {
		return fmt.Errorf("clear xiangwan cover GC marker: %w", err)
	}
	return nil
}

// MarkDeleted leaves a durable tombstone before the immutable object is
// removed. Presentation writes reject that tombstone while holding the same
// advisory lock, so a stale URL cannot become a live database reference even
// if filesystem deletion is interrupted.
func (store *CoverGCMarkerStore) MarkDeleted(
	ctx context.Context,
	database RowExecer,
	filename string,
) error {
	if store == nil || ctx == nil || database == nil ||
		!coverImageFilenamePattern.MatchString(filename) {
		return ErrInvalidCoverGCMarkerQuery
	}
	_, err := database.ExecContext(ctx, `
INSERT INTO xiangwan_cover_gc_markers (filename, orphaned_at, deleted_at)
VALUES ($1, NOW(), NOW())
ON CONFLICT (filename) DO UPDATE
SET deleted_at = COALESCE(xiangwan_cover_gc_markers.deleted_at, EXCLUDED.deleted_at)
`, filename)
	if err != nil {
		return fmt.Errorf("mark xiangwan deleted cover: %w", err)
	}
	return nil
}
