package publicobject

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrInvalidReader         = errors.New("invalid public object reader")
	ErrObjectNotFound        = errors.New("confirmed public object not found")
	ErrObjectFactsConflict   = errors.New("public object facts conflict")
	ErrObjectReadUnavailable = errors.New("public object read unavailable")
)

type Object struct {
	FileID       uuid.UUID
	MIME         string
	DetectedMIME string
	Size         int64
	Content      io.ReadSeeker
	Closer       io.Closer
}

func (object *Object) Read(buffer []byte) (int, error) {
	if object == nil || object.Content == nil {
		return 0, ErrInvalidReader
	}
	return object.Content.Read(buffer)
}

func (object *Object) Seek(offset int64, whence int) (int64, error) {
	if object == nil || object.Content == nil {
		return 0, ErrInvalidReader
	}
	return object.Content.Seek(offset, whence)
}

func (object *Object) Close() error {
	if object == nil || object.Closer == nil {
		return nil
	}
	return object.Closer.Close()
}

type Reader interface {
	OpenConfirmedObject(context.Context, uuid.UUID) (*Object, error)
}

type objectRowScanner interface {
	Scan(...any) error
}

type objectQueryExecutor interface {
	queryRowContext(
		context.Context,
		string,
		...any,
	) objectRowScanner
}

type sqlObjectQueryExecutor struct {
	database *sql.DB
}

func (executor sqlObjectQueryExecutor) queryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) objectRowScanner {
	return executor.database.QueryRowContext(ctx, query, args...)
}

type LocalReader struct {
	database objectQueryExecutor
	root     string
}

func NewLocalReader(database *sql.DB, localDirectory string) (*LocalReader, error) {
	if database == nil {
		return nil, ErrInvalidReader
	}
	root, err := canonicalStorageRoot(localDirectory)
	if err != nil {
		return nil, ErrInvalidReader
	}
	return &LocalReader{
		database: sqlObjectQueryExecutor{database: database},
		root:     root,
	}, nil
}

const confirmedObjectSelect = `
SELECT
    file_key,
    LOWER(BTRIM(mime)),
    size
FROM files
WHERE id = $1
  AND status = 'confirmed'
  AND size > 0
  AND delete_after IS NULL
  AND expired_at IS NULL
LIMIT 1
`

func (reader *LocalReader) OpenConfirmedObject(
	ctx context.Context,
	fileID uuid.UUID,
) (*Object, error) {
	if reader == nil || reader.database == nil || reader.root == "" ||
		ctx == nil || fileID == uuid.Nil {
		return nil, ErrInvalidReader
	}
	var fileKey string
	var storedMIME string
	var storedSize int64
	if err := reader.database.queryRowContext(
		ctx,
		confirmedObjectSelect,
		fileID,
	).Scan(&fileKey, &storedMIME, &storedSize); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrObjectNotFound
		}
		return nil, ErrObjectReadUnavailable
	}
	storedMIME, err := canonicalMIME(storedMIME)
	if err != nil || storedSize <= 0 {
		return nil, ErrObjectFactsConflict
	}
	objectPath, err := resolveObjectPath(reader.root, fileKey)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrObjectNotFound
		}
		return nil, ErrObjectFactsConflict
	}
	handle, err := os.Open(objectPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrObjectNotFound
		}
		return nil, ErrObjectReadUnavailable
	}
	closeOnError := func() { _ = handle.Close() }
	info, err := handle.Stat()
	if err != nil {
		closeOnError()
		return nil, ErrObjectReadUnavailable
	}
	if !info.Mode().IsRegular() || info.Size() != storedSize {
		closeOnError()
		return nil, ErrObjectFactsConflict
	}
	prefix := make([]byte, 512)
	read, readErr := io.ReadFull(handle, prefix)
	if readErr != nil && !errors.Is(readErr, io.EOF) &&
		!errors.Is(readErr, io.ErrUnexpectedEOF) {
		closeOnError()
		return nil, ErrObjectReadUnavailable
	}
	if _, err := handle.Seek(0, io.SeekStart); err != nil {
		closeOnError()
		return nil, ErrObjectReadUnavailable
	}
	return &Object{
		FileID:       fileID,
		MIME:         storedMIME,
		DetectedMIME: canonicalDetectedMIME(http.DetectContentType(prefix[:read])),
		Size:         storedSize,
		Content:      handle,
		Closer:       handle,
	}, nil
}

func canonicalStorageRoot(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", ErrInvalidReader
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", ErrInvalidReader
	}
	return filepath.Clean(resolved), nil
}

func resolveObjectPath(root string, fileKey string) (string, error) {
	normalized := filepath.FromSlash(strings.TrimSpace(fileKey))
	if normalized == "" || filepath.IsAbs(normalized) ||
		filepath.VolumeName(normalized) != "" {
		return "", ErrObjectFactsConflict
	}
	candidate, err := filepath.Abs(filepath.Join(root, normalized))
	if err != nil {
		return "", err
	}
	if !pathWithinRoot(root, candidate) {
		return "", ErrObjectFactsConflict
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	if !pathWithinRoot(root, resolved) {
		return "", ErrObjectFactsConflict
	}
	return resolved, nil
}

func pathWithinRoot(root string, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func canonicalMIME(value string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(strings.ToLower(strings.TrimSpace(value)))
	if err != nil || mediaType == "" || mediaType == "text/html" ||
		mediaType == "image/svg+xml" {
		return "", ErrObjectFactsConflict
	}
	return mediaType, nil
}

func canonicalDetectedMIME(value string) string {
	mediaType, _, err := mime.ParseMediaType(strings.ToLower(strings.TrimSpace(value)))
	if err != nil {
		return ""
	}
	return mediaType
}

var _ io.ReadSeeker = (*Object)(nil)
var _ io.Closer = (*Object)(nil)
var _ Reader = (*LocalReader)(nil)
