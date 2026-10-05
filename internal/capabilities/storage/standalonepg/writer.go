// Package standalonepg exposes the narrow PostgreSQL file metadata writer used
// by independently deployed products. It deliberately has no dependency on
// the Storage HTTP service, Content, middleware, cache, event bus, or Redis.
//
// The caller owns the transaction. A product must stage the object and verify
// its provider facts before calling Confirm, then call Pin in the same
// transaction as its durable business reference. The package never returns a
// provider object key or filesystem path.
package standalonepg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"mime"
	"path"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/pkg/fileurl"

	"github.com/google/uuid"
)

const (
	// DefaultPendingRetention is the cleanup window used when a caller does
	// not provide one. Pin removes this deadline after the business binding
	// has been written in the same caller transaction.
	DefaultPendingRetention = 72 * time.Hour

	maxFilenameLength  = 500
	maxMIMETypeLength  = 100
	maxObjectKeyLength = 500
)

var (
	ErrInvalidWriter       = errors.New("invalid Storage standalone PostgreSQL writer")
	ErrInvalidFile         = errors.New("invalid Storage file input")
	ErrFileNotFound        = errors.New("Storage file not found")
	ErrFileForbidden       = errors.New("Storage file owner mismatch")
	ErrFileConflict        = errors.New("Storage file lifecycle conflict")
	ErrFileNotConfirmed    = errors.New("Storage file is not confirmed")
	ErrFileExpired         = errors.New("Storage file lifecycle has ended")
	ErrProviderObjectFacts = errors.New("Storage provider object facts conflict")
)

// File is the safe metadata projection returned to a product. Object keys,
// CDN URLs, metadata internals, and filesystem paths intentionally do not cross
// this seam.
type File struct {
	ID          uuid.UUID
	PrincipalID uuid.UUID
	Filename    string
	MIME        string
	Size        int64
	Status      string
	CreatedAt   time.Time
	DeleteAfter *time.Time
	DeletingAt  *time.Time
	ExpiredAt   *time.Time
}

// PendingFileInput describes a row created before the provider object is
// staged. ProviderObjectKey is accepted only to bind the later confirmation;
// it is never returned by this package.
type PendingFileInput struct {
	ID                uuid.UUID
	PrincipalID       uuid.UUID
	Filename          string
	MIME              string
	Size              int64
	ProviderObjectKey string
	DeleteAfter       *time.Time
	CreatedAt         time.Time
}

// ProviderObjectFacts are produced by the provider-owned staging/inspection
// adapter. The adapter must verify the actual object bytes before passing these
// facts to Confirm. This package verifies that the facts match the pending row
// while holding its PostgreSQL row lock.
type ProviderObjectFacts struct {
	ProviderObjectKey string
	MIME              string
	Size              int64
}

// Writer is bound to a caller-owned SQL transaction. It never begins or
// commits a transaction, allowing a product's operation, audit, outbox, and
// resource relation to commit atomically with the File lifecycle transition.
type Writer struct {
	tx *sql.Tx
}

// NewWriter constructs a transaction-bound writer.
func NewWriter(tx *sql.Tx) (*Writer, error) {
	if tx == nil {
		return nil, ErrInvalidWriter
	}
	return &Writer{tx: tx}, nil
}

// CreatePending creates or replays one pending file row. ID is the canonical
// UUIDv4 upload identity. A replay succeeds only when the immutable owner,
// name, MIME, size, and provider-key facts match. The cleanup deadline remains
// server-owned and is never changed by a replay.
func (writer *Writer) CreatePending(ctx context.Context, input PendingFileInput) (File, error) {
	if !writer.valid(ctx) {
		return File{}, ErrInvalidWriter
	}
	normalized, err := normalizePendingInput(input)
	if err != nil {
		return File{}, err
	}
	createdAt := normalized.CreatedAt
	deleteAfter := normalized.DeleteAfter
	_, err = writer.tx.ExecContext(ctx, `
INSERT INTO files (
    id, principal_id, filename, mime, size, file_key, cdn_url, status,
    created_at, metadata, delete_after
) VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8, '{}'::jsonb, $9)
ON CONFLICT (id) DO NOTHING
`, normalized.ID, normalized.PrincipalID, normalized.Filename, normalized.MIME,
		normalized.Size, normalized.ProviderObjectKey,
		fileurl.BuildAccessPath(normalized.ID, nil), createdAt, deleteAfter)
	if err != nil {
		return File{}, fmt.Errorf("create Storage pending file: %w", err)
	}
	file, err := writer.lockFile(ctx, normalized.ID)
	if err != nil {
		return File{}, err
	}
	if err := requireLive(file); err != nil {
		return File{}, err
	}
	storedKey, err := writer.lockedObjectKey(ctx, normalized.ID)
	if err != nil {
		return File{}, err
	}
	if storedKey != normalized.ProviderObjectKey {
		return File{}, ErrFileConflict
	}
	if file.Status == "pending" {
		if !samePendingIdentity(file, normalized) {
			return File{}, ErrFileConflict
		}
	} else if file.Status == "confirmed" {
		// A committed confirmation/pin may be replayed with the same immutable
		// identity. Its cleanup deadline is allowed to have changed because Pin
		// deliberately clears it.
		if !sameImmutableIdentity(file, normalized) {
			return File{}, ErrFileConflict
		}
	} else {
		return File{}, ErrFileConflict
	}
	return file, nil
}

// Confirm verifies provider-owned object facts and transitions an owned
// pending row to confirmed. Replaying the same confirmation is idempotent;
// changed facts or a terminal lifecycle are rejected under the row lock.
func (writer *Writer) Confirm(ctx context.Context, principalID, fileID uuid.UUID, facts ProviderObjectFacts) (File, error) {
	if !writer.valid(ctx) || principalID == uuid.Nil || fileID == uuid.Nil {
		return File{}, ErrInvalidWriter
	}
	normalizedFacts, err := normalizeProviderObjectFacts(facts)
	if err != nil {
		return File{}, err
	}
	file, err := writer.lockFile(ctx, fileID)
	if err != nil {
		return File{}, err
	}
	if file.PrincipalID != principalID {
		return File{}, ErrFileForbidden
	}
	if err := requireLive(file); err != nil {
		return File{}, err
	}
	storedKey, err := writer.lockedObjectKey(ctx, fileID)
	if err != nil {
		return File{}, err
	}
	if storedKey != normalizedFacts.ProviderObjectKey ||
		file.MIME != normalizedFacts.MIME || file.Size != normalizedFacts.Size {
		return File{}, ErrProviderObjectFacts
	}
	if file.Status == "confirmed" {
		return file, nil
	}
	if file.Status != "pending" {
		return File{}, ErrFileNotConfirmed
	}
	result, err := writer.tx.ExecContext(ctx, `
UPDATE files
SET status = 'confirmed'
WHERE id = $1 AND principal_id = $2 AND status = 'pending'
  AND (delete_after IS NULL OR delete_after > CURRENT_TIMESTAMP)
`, fileID, principalID)
	if err != nil {
		return File{}, fmt.Errorf("confirm Storage file: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return File{}, ErrFileConflict
	}
	file.Status = "confirmed"
	return file, nil
}

// Pin clears the cleanup deadline from an owned confirmed file. The caller
// should invoke it in the same transaction as its durable relation/reference.
// Pin is idempotent for an already permanent file, but still requires owner
// equality so a product cannot use this seam as a cross-owner retention grant.
func (writer *Writer) Pin(ctx context.Context, principalID, fileID uuid.UUID) (File, error) {
	if !writer.valid(ctx) || principalID == uuid.Nil || fileID == uuid.Nil {
		return File{}, ErrInvalidWriter
	}
	file, err := writer.lockFile(ctx, fileID)
	if err != nil {
		return File{}, err
	}
	if file.PrincipalID != principalID {
		return File{}, ErrFileForbidden
	}
	if err := requireLive(file); err != nil {
		return File{}, err
	}
	if file.Status != "confirmed" {
		return File{}, ErrFileNotConfirmed
	}
	if file.DeleteAfter == nil {
		return file, nil
	}
	result, err := writer.tx.ExecContext(ctx, `
UPDATE files
SET delete_after = NULL,
    metadata = metadata - 'delete_after'
WHERE id = $1 AND principal_id = $2 AND status = 'confirmed'
  AND delete_after IS NOT NULL
`, fileID, principalID)
	if err != nil {
		return File{}, fmt.Errorf("pin Storage file: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return File{}, ErrFileConflict
	}
	file.DeleteAfter = nil
	return file, nil
}

// GetForStaging checks the current private upload lifecycle under the File lock.
// A remembered upload intent is not permission to recreate cleaned-up bytes.
func (writer *Writer) GetForStaging(ctx context.Context, principalID, fileID uuid.UUID) (File, error) {
	if !writer.valid(ctx) || principalID == uuid.Nil || fileID == uuid.Nil {
		return File{}, ErrInvalidWriter
	}
	file, err := writer.lockFile(ctx, fileID)
	if err != nil {
		return File{}, err
	}
	if file.PrincipalID != principalID {
		return File{}, ErrFileForbidden
	}
	if err := requireLive(file); err != nil {
		return File{}, err
	}
	if file.Status != "pending" && file.Status != "confirmed" {
		return File{}, ErrFileConflict
	}
	return file, nil
}

// GetConfirmed reads an owned, permanent confirmed file while holding a
// PostgreSQL row lock for the duration of the caller transaction.
func (writer *Writer) GetConfirmed(ctx context.Context, principalID, fileID uuid.UUID) (File, error) {
	if !writer.valid(ctx) || principalID == uuid.Nil || fileID == uuid.Nil {
		return File{}, ErrInvalidWriter
	}
	file, err := writer.lockFile(ctx, fileID)
	if err != nil {
		return File{}, err
	}
	if file.PrincipalID != principalID {
		return File{}, ErrFileForbidden
	}
	if err := requireLive(file); err != nil {
		return File{}, err
	}
	if file.Status != "confirmed" {
		return File{}, ErrFileNotConfirmed
	}
	if file.DeleteAfter != nil {
		return File{}, ErrFileConflict
	}
	return file, nil
}

// CanonicalMIME validates and normalizes one stored MIME value. It rejects
// HTML and SVG active-content types at the provider seam.
func CanonicalMIME(value string) (string, error) {
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", ErrInvalidFile
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxMIMETypeLength {
		return "", ErrInvalidFile
	}
	mediaType, _, err := mime.ParseMediaType(strings.ToLower(value))
	if err != nil || mediaType == "" || mediaType == "text/html" || mediaType == "image/svg+xml" {
		return "", ErrInvalidFile
	}
	return mediaType, nil
}

// CanonicalVideoMIME is the review-media-specific MIME gate. The generic
// writer remains usable for non-video platform files, while Xiangwan review
// adapters can require this helper before creating a pending row.
func CanonicalVideoMIME(value string) (string, error) {
	mediaType, err := CanonicalMIME(value)
	if err != nil || !strings.HasPrefix(mediaType, "video/") {
		return "", ErrInvalidFile
	}
	return mediaType, nil
}

func (writer *Writer) valid(ctx context.Context) bool {
	return writer != nil && writer.tx != nil && ctx != nil
}

func (writer *Writer) lockFile(ctx context.Context, fileID uuid.UUID) (File, error) {
	var file File
	var deleteAfter, deletingAt, expiredAt sql.NullTime
	err := writer.tx.QueryRowContext(ctx, `
SELECT id, principal_id, filename, LOWER(BTRIM(mime)), size, status,
       created_at, delete_after, deleting_at, expired_at
FROM files
WHERE id = $1
FOR UPDATE
`, fileID).Scan(
		&file.ID, &file.PrincipalID, &file.Filename, &file.MIME, &file.Size,
		&file.Status, &file.CreatedAt, &deleteAfter, &deletingAt, &expiredAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrFileNotFound
	}
	if err != nil {
		return File{}, fmt.Errorf("lock Storage file: %w", err)
	}
	file.DeleteAfter = nullTimePointer(deleteAfter)
	file.DeletingAt = nullTimePointer(deletingAt)
	file.ExpiredAt = nullTimePointer(expiredAt)
	return file, nil
}

func (writer *Writer) lockedObjectKey(ctx context.Context, fileID uuid.UUID) (string, error) {
	var objectKey string
	if err := writer.tx.QueryRowContext(ctx,
		"SELECT file_key FROM files WHERE id = $1 FOR UPDATE", fileID).Scan(&objectKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrFileNotFound
		}
		return "", fmt.Errorf("read Storage provider object key: %w", err)
	}
	return objectKey, nil
}

func normalizePendingInput(input PendingFileInput) (PendingFileInput, error) {
	if input.ID == uuid.Nil || input.ID.Version() != 4 || input.ID.Variant() != uuid.RFC4122 ||
		input.PrincipalID == uuid.Nil {
		return PendingFileInput{}, ErrInvalidFile
	}
	if input.Filename != strings.TrimSpace(input.Filename) || input.Filename == "" ||
		len(input.Filename) > maxFilenameLength || strings.ContainsAny(input.Filename, "\x00\r\n") {
		return PendingFileInput{}, ErrInvalidFile
	}
	mimeType, err := CanonicalMIME(input.MIME)
	if err != nil {
		return PendingFileInput{}, err
	}
	objectKey := input.ProviderObjectKey
	if err := validateObjectKey(objectKey); err != nil {
		return PendingFileInput{}, err
	}
	if input.Size <= 0 {
		return PendingFileInput{}, ErrInvalidFile
	}
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	} else {
		createdAt = createdAt.UTC()
	}
	deleteAfter := input.DeleteAfter
	if deleteAfter == nil {
		deadline := createdAt.Add(DefaultPendingRetention)
		deleteAfter = &deadline
	} else {
		deadline := deleteAfter.UTC()
		if !deadline.After(createdAt) {
			return PendingFileInput{}, ErrInvalidFile
		}
		deleteAfter = &deadline
	}
	input.MIME = mimeType
	input.ProviderObjectKey = objectKey
	input.CreatedAt = createdAt
	input.DeleteAfter = deleteAfter
	return input, nil
}

func normalizeProviderObjectFacts(facts ProviderObjectFacts) (ProviderObjectFacts, error) {
	if err := validateObjectKey(facts.ProviderObjectKey); err != nil || facts.Size <= 0 {
		return ProviderObjectFacts{}, ErrProviderObjectFacts
	}
	mimeType, err := CanonicalMIME(facts.MIME)
	if err != nil {
		return ProviderObjectFacts{}, ErrProviderObjectFacts
	}
	facts.MIME = mimeType
	return facts, nil
}

func samePendingIdentity(file File, input PendingFileInput) bool {
	return sameImmutableIdentity(file, input)
}

func sameImmutableIdentity(file File, input PendingFileInput) bool {
	if file.ID != input.ID || file.PrincipalID != input.PrincipalID ||
		file.Filename != input.Filename || file.MIME != input.MIME || file.Size != input.Size {
		return false
	}
	return true
}

func requireLive(file File) error {
	if file.DeletingAt != nil || file.ExpiredAt != nil || file.Status == "deleting" ||
		file.Status == "expired" || file.Status == "cleanup_failed" {
		return ErrFileExpired
	}
	if file.DeleteAfter != nil && !file.DeleteAfter.After(time.Now().UTC()) {
		return ErrFileExpired
	}
	return nil
}

func validateObjectKey(value string) error {
	if value != strings.TrimSpace(value) || value == "" ||
		len(value) > maxObjectKeyLength || strings.ContainsAny(value, "\x00\r\n\\") {
		return ErrInvalidFile
	}
	normalizedPath := strings.ReplaceAll(value, "\\", "/")
	if path.IsAbs(normalizedPath) || strings.HasPrefix(normalizedPath, "//") ||
		strings.Contains(normalizedPath, "://") ||
		(len(normalizedPath) >= 2 && normalizedPath[1] == ':') {
		return ErrInvalidFile
	}
	for _, segment := range strings.Split(normalizedPath, "/") {
		if segment == ".." {
			return ErrInvalidFile
		}
	}
	return nil
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func requireOneRow(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return ErrFileConflict
	}
	return nil
}
