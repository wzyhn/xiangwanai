package publicobject

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestLocalReaderOpensExactConfirmedObject(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	objectBytes := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	objectPath := filepath.Join(root, "uploads", "photo.png")
	if err := os.MkdirAll(filepath.Dir(objectPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(objectPath, objectBytes, 0o600); err != nil {
		t.Fatalf("write object: %v", err)
	}
	fileID := uuid.New()
	var capturedArgs []any
	reader := &LocalReader{
		database: fakeObjectQueryExecutor{query: func(
			_ string,
			args ...any,
		) objectRowScanner {
			capturedArgs = append([]any(nil), args...)
			return fakeObjectRow{values: []any{
				"uploads/photo.png",
				"IMAGE/PNG",
				int64(len(objectBytes)),
			}}
		}},
		root: root,
	}
	object, err := reader.OpenConfirmedObject(context.Background(), fileID)
	if err != nil {
		t.Fatalf("OpenConfirmedObject() error = %v", err)
	}
	defer func() { _ = object.Close() }()
	if object.FileID != fileID || object.MIME != "image/png" ||
		object.DetectedMIME != "image/png" || object.Size != int64(len(objectBytes)) ||
		!reflect.DeepEqual(capturedArgs, []any{fileID}) {
		t.Fatalf("OpenConfirmedObject() = %+v args=%#v", object, capturedArgs)
	}
	read := make([]byte, len(objectBytes))
	if _, err := object.Read(read); err != nil || !reflect.DeepEqual(read, objectBytes) {
		t.Fatalf("read object = %v, %v", read, err)
	}
}

func TestLocalReaderRejectsUnsafeOrDriftedObject(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	inside := filepath.Join(root, "inside.bin")
	if err := os.WriteFile(inside, []byte("inside"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tests := []struct {
		name   string
		values []any
		want   error
	}{
		{name: "missing", values: nil, want: ErrObjectNotFound},
		{name: "path escape", values: []any{"../outside.bin", "image/png", int64(8)}, want: ErrObjectFactsConflict},
		{name: "size drift", values: []any{"inside.bin", "application/octet-stream", int64(99)}, want: ErrObjectFactsConflict},
		{name: "active mime", values: []any{"inside.bin", "text/html", int64(6)}, want: ErrObjectFactsConflict},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			row := objectRowScanner(fakeObjectRow{values: test.values})
			if test.values == nil {
				row = fakeObjectRow{err: sql.ErrNoRows}
			}
			reader := &LocalReader{
				database: fakeObjectQueryExecutor{query: func(string, ...any) objectRowScanner {
					return row
				}},
				root: root,
			}
			_, err := reader.OpenConfirmedObject(context.Background(), uuid.New())
			if !errors.Is(err, test.want) || strings.Contains(err.Error(), "outside.bin") {
				t.Fatalf("OpenConfirmedObject() error = %v", err)
			}
		})
	}
}

func TestLocalReaderRequiresExistingDirectory(t *testing.T) {
	t.Parallel()

	if _, err := NewLocalReader(nil, "missing"); !errors.Is(err, ErrInvalidReader) {
		t.Fatalf("NewLocalReader(nil) error = %v", err)
	}
}

type fakeObjectQueryExecutor struct {
	query func(string, ...any) objectRowScanner
}

func (executor fakeObjectQueryExecutor) queryRowContext(
	_ context.Context,
	query string,
	args ...any,
) objectRowScanner {
	return executor.query(query, args...)
}

type fakeObjectRow struct {
	values []any
	err    error
}

func (row fakeObjectRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != len(row.values) {
		return errors.New("object row destination mismatch")
	}
	for index, destination := range destinations {
		switch target := destination.(type) {
		case *string:
			*target = row.values[index].(string)
		case *int64:
			*target = row.values[index].(int64)
		default:
			return errors.New("unsupported object row destination")
		}
	}
	return nil
}
