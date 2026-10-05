package standalonepg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ReviewLocalStager is the Storage-owned local-byte adapter for an isolated
// product runtime. It only accepts server-derived file IDs and never exposes
// a caller-controlled filesystem path. Its immutable object selection is
// separate from the caller's pending/confirmed PostgreSQL transaction.
type ReviewLocalStager struct {
	root string
}

type ReviewObjectFacts struct {
	ProviderObjectKey string
	MIME              string
	Size              int64
	SHA256            string
}

var (
	ErrInvalidReviewObject  = errors.New("invalid review object input")
	ErrReviewObjectConflict = errors.New("review object facts conflict")
	ErrReviewObjectMissing  = errors.New("review object missing")
)

const MaxReviewObjectBytes int64 = 200 << 20

// A temporary upload has no durable File identity until Stage selects its
// UUID-named object. An abandoned temporary can be pruned after the same
// three-day minimum used for unpinned File retention.
const reviewTemporaryRetention = 72 * time.Hour

var reviewMIMEs = map[string]struct{}{
	"image/jpeg": {}, "image/png": {}, "image/webp": {},
	"video/mp4": {}, "audio/mpeg": {}, "application/pdf": {},
}

// NewReviewLocalStager uses a fixed, private subdirectory of the runtime's
// existing Storage root. The root must already exist, as it does for the
// confirmed-object reader. A symlinked subdirectory is rejected.
func NewReviewLocalStager(storageRoot string) (*ReviewLocalStager, error) {
	storageRoot = strings.TrimSpace(storageRoot)
	absolute, err := filepath.Abs(storageRoot)
	if err != nil || storageRoot == "" {
		return nil, ErrInvalidReviewObject
	}
	canonicalRoot, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, ErrInvalidReviewObject
	}
	rootInfo, err := os.Stat(canonicalRoot)
	if err != nil || !rootInfo.IsDir() {
		return nil, ErrInvalidReviewObject
	}
	directory := filepath.Join(canonicalRoot, "xiangwan-review")
	if err := os.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidReviewObject
	}
	return &ReviewLocalStager{root: directory}, nil
}

// ReviewObjectKey is an opaque Storage provider key, never a user path.
func ReviewObjectKey(fileID uuid.UUID) string {
	if fileID == uuid.Nil || fileID.Version() != 4 || fileID.Variant() != uuid.RFC4122 {
		return ""
	}
	return "xiangwan-review/" + fileID.String()
}

// PruneStaleTemporaries removes only abandoned CreateTemp names in the private
// review directory. Selected UUID objects are never deleted by this method;
// their lifecycle must be coordinated with PostgreSQL File state. The caller
// supplies a trusted clock and may invoke this on startup or from a worker.
func (stager *ReviewLocalStager) PruneStaleTemporaries(
	ctx context.Context, now time.Time,
) (int, error) {
	if stager == nil || stager.root == "" || ctx == nil || now.IsZero() {
		return 0, ErrInvalidReviewObject
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	info, err := os.Lstat(stager.root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, ErrInvalidReviewObject
	}
	entries, err := os.ReadDir(stager.root)
	if err != nil {
		return 0, err
	}
	cutoff := now.UTC().Add(-reviewTemporaryRetention)
	removed := 0
	var failures []error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, errors.Join(append(failures, err)...)
		}
		if !strings.HasPrefix(entry.Name(), ".review-upload-") ||
			len(entry.Name()) <= len(".review-upload-") {
			continue
		}
		entryInfo, err := entry.Info()
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !entryInfo.Mode().IsRegular() || !entryInfo.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(stager.root, entry.Name())); err != nil {
			failures = append(failures, fmt.Errorf("remove stale review temporary: %w", err))
			continue
		}
		removed++
	}
	return removed, errors.Join(failures...)
}

// DeleteSelected is the provider phase of File cleanup. Call it only after a
// PostgreSQL lifecycle claim has changed the exact File to deleting. A missing
// object is an idempotent success for crash recovery; symlinks and unexpected
// filesystem nodes are never followed or removed.
func (stager *ReviewLocalStager) DeleteSelected(
	ctx context.Context, fileID uuid.UUID,
) error {
	if stager == nil || stager.root == "" || ctx == nil ||
		ReviewObjectKey(fileID) == "" {
		return ErrInvalidReviewObject
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	rootInfo, err := os.Lstat(stager.root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidReviewObject
	}
	path := filepath.Join(stager.root, fileID.String())
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrReviewObjectConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete selected review object: %w", err)
	}
	return nil
}

// Stage streams an exact-size object into a request-unique temporary file,
// then selects the durable object with an atomic no-replace hard link. A retry
// can observe the same bytes, but can never overwrite a selected object.
func (stager *ReviewLocalStager) Stage(
	ctx context.Context, fileID uuid.UUID, source io.Reader,
	expectedMIME string, expectedSize int64, expectedSHA256 string,
) (ReviewObjectFacts, error) {
	if stager == nil || stager.root == "" || ctx == nil || source == nil ||
		ReviewObjectKey(fileID) == "" || expectedSize <= 0 ||
		expectedSize > MaxReviewObjectBytes || !validReviewSHA(expectedSHA256) {
		return ReviewObjectFacts{}, ErrInvalidReviewObject
	}
	canonicalMIME, err := CanonicalMIME(expectedMIME)
	if err != nil || !allowedReviewMIME(canonicalMIME) {
		return ReviewObjectFacts{}, ErrInvalidReviewObject
	}
	if err := ctx.Err(); err != nil {
		return ReviewObjectFacts{}, err
	}
	temporary, err := os.CreateTemp(stager.root, ".review-upload-*")
	if err != nil {
		return ReviewObjectFacts{}, err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return ReviewObjectFacts{}, err
	}
	hasher := sha256.New()
	limited := &io.LimitedReader{R: source, N: expectedSize + 1}
	written, err := io.Copy(io.MultiWriter(temporary, hasher), &contextReader{ctx: ctx, source: limited})
	if err != nil {
		_ = temporary.Close()
		return ReviewObjectFacts{}, err
	}
	if written != expectedSize || hex.EncodeToString(hasher.Sum(nil)) != expectedSHA256 {
		_ = temporary.Close()
		return ReviewObjectFacts{}, ErrReviewObjectConflict
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return ReviewObjectFacts{}, err
	}
	if err := temporary.Close(); err != nil {
		return ReviewObjectFacts{}, err
	}
	if _, err := inspectReviewObject(ctx, temporaryPath, fileID, canonicalMIME, expectedSize, expectedSHA256); err != nil {
		return ReviewObjectFacts{}, err
	}
	if err := ctx.Err(); err != nil {
		return ReviewObjectFacts{}, err
	}
	finalPath := filepath.Join(stager.root, fileID.String())
	if err := os.Link(temporaryPath, finalPath); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return ReviewObjectFacts{}, err
		}
		// A concurrent winner or a replay may have selected this ID. Compare
		// the complete bytes; a different object is an immutable conflict.
		return stager.Inspect(ctx, fileID, canonicalMIME, expectedSize, expectedSHA256)
	}
	return ReviewObjectFacts{
		ProviderObjectKey: ReviewObjectKey(fileID), MIME: canonicalMIME,
		Size: expectedSize, SHA256: expectedSHA256,
	}, nil
}

// Inspect checks the selected regular file and actual bytes immediately before
// a caller confirms metadata. The caller still rechecks its owner, generation,
// expiry and pending File row in its own transaction.
func (stager *ReviewLocalStager) Inspect(
	ctx context.Context, fileID uuid.UUID, expectedMIME string,
	expectedSize int64, expectedSHA256 string,
) (ReviewObjectFacts, error) {
	if stager == nil || stager.root == "" || ctx == nil || ReviewObjectKey(fileID) == "" ||
		expectedSize <= 0 || expectedSize > MaxReviewObjectBytes || !validReviewSHA(expectedSHA256) {
		return ReviewObjectFacts{}, ErrInvalidReviewObject
	}
	canonicalMIME, err := CanonicalMIME(expectedMIME)
	if err != nil || !allowedReviewMIME(canonicalMIME) {
		return ReviewObjectFacts{}, ErrInvalidReviewObject
	}
	if err := ctx.Err(); err != nil {
		return ReviewObjectFacts{}, err
	}
	path := filepath.Join(stager.root, fileID.String())
	return inspectReviewObject(ctx, path, fileID, canonicalMIME, expectedSize, expectedSHA256)
}

// OpenSelected opens a private selected object for an authorized caller only
// after checking its complete immutable byte identity. The caller owns the
// returned descriptor and must close it. No provider path leaves Storage.
func (stager *ReviewLocalStager) OpenSelected(
	ctx context.Context, fileID uuid.UUID, expectedMIME string,
	expectedSize int64, expectedSHA256 string,
) (*os.File, error) {
	if stager == nil || stager.root == "" || ctx == nil || ReviewObjectKey(fileID) == "" ||
		expectedSize <= 0 || expectedSize > MaxReviewObjectBytes || !validReviewSHA(expectedSHA256) {
		return nil, ErrInvalidReviewObject
	}
	canonicalMIME, err := CanonicalMIME(expectedMIME)
	if err != nil || !allowedReviewMIME(canonicalMIME) {
		return nil, ErrInvalidReviewObject
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, _, err := openVerifiedReviewObject(ctx,
		filepath.Join(stager.root, fileID.String()), fileID,
		canonicalMIME, expectedSize, expectedSHA256)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func inspectReviewObject(ctx context.Context, path string, fileID uuid.UUID, expectedMIME string, expectedSize int64, expectedSHA256 string) (ReviewObjectFacts, error) {
	file, facts, err := openVerifiedReviewObject(ctx, path, fileID,
		expectedMIME, expectedSize, expectedSHA256)
	if err != nil {
		return ReviewObjectFacts{}, err
	}
	defer func() { _ = file.Close() }()
	return facts, nil
}

func openVerifiedReviewObject(
	ctx context.Context, path string, fileID uuid.UUID,
	expectedMIME string, expectedSize int64, expectedSHA256 string,
) (*os.File, ReviewObjectFacts, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ReviewObjectFacts{}, ErrReviewObjectMissing
	}
	if err != nil {
		return nil, ReviewObjectFacts{}, err
	}
	if !info.Mode().IsRegular() || info.Size() != expectedSize {
		return nil, ReviewObjectFacts{}, ErrReviewObjectConflict
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ReviewObjectFacts{}, err
	}
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() ||
		openedInfo.Size() != expectedSize || !os.SameFile(info, openedInfo) {
		_ = file.Close()
		return nil, ReviewObjectFacts{}, ErrReviewObjectConflict
	}
	prefix := make([]byte, 512)
	read, readErr := io.ReadFull(file, prefix)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		_ = file.Close()
		return nil, ReviewObjectFacts{}, readErr
	}
	if http.DetectContentType(prefix[:read]) != expectedMIME {
		_ = file.Close()
		return nil, ReviewObjectFacts{}, ErrReviewObjectConflict
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, ReviewObjectFacts{}, err
	}
	hasher := sha256.New()
	actualSize, err := io.Copy(hasher, &contextReader{ctx: ctx, source: file})
	if err != nil {
		_ = file.Close()
		return nil, ReviewObjectFacts{}, err
	}
	if actualSize != expectedSize || hex.EncodeToString(hasher.Sum(nil)) != expectedSHA256 {
		_ = file.Close()
		return nil, ReviewObjectFacts{}, ErrReviewObjectConflict
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, ReviewObjectFacts{}, err
	}
	return file, ReviewObjectFacts{
		ProviderObjectKey: ReviewObjectKey(fileID), MIME: expectedMIME,
		Size: expectedSize, SHA256: expectedSHA256,
	}, nil
}

func allowedReviewMIME(value string) bool {
	_, allowed := reviewMIMEs[value]
	return allowed
}

func validReviewSHA(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(buffer)
}
