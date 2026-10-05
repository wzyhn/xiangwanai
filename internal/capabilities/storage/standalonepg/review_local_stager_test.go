package standalonepg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
)

func reviewTestPNG() []byte {
	return append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{0}, 64)...)
}

func reviewTestSHA(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func TestReviewLocalStagerSelectsImmutableVerifiedBytes(t *testing.T) {
	stager, err := NewReviewLocalStager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fileID := uuid.New()
	data := reviewTestPNG()
	checksum := reviewTestSHA(data)
	stage := func(data []byte, digest string) (ReviewObjectFacts, error) {
		return stager.Stage(context.Background(), fileID, bytes.NewReader(data),
			"image/png", int64(len(data)), digest)
	}
	first, err := stage(data, checksum)
	if err != nil || first.ProviderObjectKey != ReviewObjectKey(fileID) ||
		first.MIME != "image/png" || first.Size != int64(len(data)) || first.SHA256 != checksum {
		t.Fatalf("selected object = %+v, %v", first, err)
	}
	if _, err := stage(data, checksum); err != nil {
		t.Fatalf("identical upload replay = %v", err)
	}
	changed := append([]byte(nil), data...)
	changed[len(changed)-1] = 1
	if _, err := stage(changed, reviewTestSHA(changed)); !errors.Is(err, ErrReviewObjectConflict) {
		t.Fatalf("different bytes under selected file ID = %v", err)
	}
	inspected, err := stager.Inspect(context.Background(), fileID, "image/png", int64(len(data)), checksum)
	if err != nil || inspected != first {
		t.Fatalf("selected original after conflict = %+v, %v", inspected, err)
	}
	info, err := os.Stat(filepath.Join(stager.root, fileID.String()))
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatalf("object permission = %v, %v", info, err)
	}
}

func TestReviewLocalStagerRejectsWrongBytesAndTampering(t *testing.T) {
	stager, err := NewReviewLocalStager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := reviewTestPNG()
	checksum := reviewTestSHA(data)
	wrongType := uuid.New()
	if _, err := stager.Stage(context.Background(), wrongType, bytes.NewReader(data),
		"application/pdf", int64(len(data)), checksum); !errors.Is(err, ErrReviewObjectConflict) {
		t.Fatalf("MIME magic mismatch = %v", err)
	}
	if _, err := stager.Inspect(context.Background(), wrongType, "application/pdf", int64(len(data)), checksum); !errors.Is(err, ErrReviewObjectMissing) {
		t.Fatalf("rejected bytes created an object: %v", err)
	}
	tooLong := uuid.New()
	if _, err := stager.Stage(context.Background(), tooLong, bytes.NewReader(append(data, 0)),
		"image/png", int64(len(data)), checksum); !errors.Is(err, ErrReviewObjectConflict) {
		t.Fatalf("oversized request = %v", err)
	}
	fileID := uuid.New()
	if _, err := stager.Stage(context.Background(), fileID, bytes.NewReader(data),
		"image/png", int64(len(data)), checksum); err != nil {
		t.Fatal(err)
	}
	changed := append([]byte(nil), data...)
	changed[len(changed)-1] = 1
	if err := os.WriteFile(filepath.Join(stager.root, fileID.String()), changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := stager.Inspect(context.Background(), fileID, "image/png", int64(len(data)), checksum); !errors.Is(err, ErrReviewObjectConflict) {
		t.Fatalf("mutated stored bytes = %v", err)
	}
}

func TestReviewLocalStagerOpenSelectedVerifiesPrivateBytes(t *testing.T) {
	stager, err := NewReviewLocalStager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fileID := uuid.New()
	data := reviewTestPNG()
	checksum := reviewTestSHA(data)
	if _, err := stager.Stage(context.Background(), fileID, bytes.NewReader(data),
		"image/png", int64(len(data)), checksum); err != nil {
		t.Fatal(err)
	}
	reader, err := stager.OpenSelected(context.Background(), fileID,
		"image/png", int64(len(data)), checksum)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(got, data) {
		t.Fatalf("private selected read = %x, %v, %v", got, readErr, closeErr)
	}
	if _, err := stager.OpenSelected(context.Background(), fileID,
		"image/png", int64(len(data)), reviewTestSHA([]byte("wrong"))); !errors.Is(err, ErrReviewObjectConflict) {
		t.Fatalf("incorrect preview checksum = %v", err)
	}
}

func TestReviewLocalStagerRejectsSymlinkedObject(t *testing.T) {
	stager, err := NewReviewLocalStager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	data := reviewTestPNG()
	if err := os.WriteFile(outside, data, 0600); err != nil {
		t.Fatal(err)
	}
	fileID := uuid.New()
	if err := os.Symlink(outside, filepath.Join(stager.root, fileID.String())); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := stager.Inspect(context.Background(), fileID, "image/png", int64(len(data)), reviewTestSHA(data)); !errors.Is(err, ErrReviewObjectConflict) {
		t.Fatalf("symlinked object = %v", err)
	}
}

func TestReviewLocalStagerPrunesOnlyOldAbandonedTemporaries(t *testing.T) {
	stager, err := NewReviewLocalStager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	old := now.Add(-reviewTemporaryRetention - time.Hour)
	stale := filepath.Join(stager.root, ".review-upload-stale")
	fresh := filepath.Join(stager.root, ".review-upload-fresh")
	selected := filepath.Join(stager.root, uuid.NewString())
	for _, path := range []string{stale, fresh, selected} {
		if err := os.WriteFile(path, []byte("private"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{stale, selected} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(fresh, now, now); err != nil {
		t.Fatal(err)
	}
	removed, err := stager.PruneStaleTemporaries(context.Background(), now)
	if err != nil || removed != 1 {
		t.Fatalf("prune = %d, %v", removed, err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temporary remains: %v", err)
	}
	for _, path := range []string{fresh, selected} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("live or selected object removed: %s: %v", path, err)
		}
	}
}

func TestReviewLocalStagerPruneSkipsSymlinkAndCanceledContext(t *testing.T) {
	stager, err := NewReviewLocalStager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("must remain"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(stager.root, ".review-upload-link")
	if err := os.Symlink(outside, link); err == nil {
		removed, pruneErr := stager.PruneStaleTemporaries(context.Background(), now)
		if pruneErr != nil || removed != 0 {
			t.Fatalf("symlink prune = %d, %v", removed, pruneErr)
		}
		if _, err := os.Stat(outside); err != nil {
			t.Fatalf("symlink target removed: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := stager.PruneStaleTemporaries(ctx, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled prune = %v", err)
	}
}

func TestReviewLocalStagerDeleteSelectedOnlyExactRegularObject(t *testing.T) {
	stager, err := NewReviewLocalStager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fileID := uuid.New()
	selected := filepath.Join(stager.root, fileID.String())
	temporary := filepath.Join(stager.root, ".review-upload-live")
	for _, path := range []string{selected, temporary} {
		if err := os.WriteFile(path, []byte("private"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for replay := 0; replay < 2; replay++ {
		if err := stager.DeleteSelected(context.Background(), fileID); err != nil {
			t.Fatalf("delete selected replay %d: %v", replay, err)
		}
	}
	if _, err := os.Stat(selected); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("selected object survived cleanup: %v", err)
	}
	if _, err := os.Stat(temporary); err != nil {
		t.Fatalf("cleanup touched request temporary: %v", err)
	}
	if err := stager.DeleteSelected(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalidReviewObject) {
		t.Fatalf("invalid File ID cleanup = %v", err)
	}
}

func TestReviewLocalStagerDeleteSelectedRejectsSymlink(t *testing.T) {
	stager, err := NewReviewLocalStager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("must remain"), 0600); err != nil {
		t.Fatal(err)
	}
	fileID := uuid.New()
	link := filepath.Join(stager.root, fileID.String())
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := stager.DeleteSelected(context.Background(), fileID); !errors.Is(err, ErrReviewObjectConflict) {
		t.Fatalf("symlink cleanup = %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("symlink target removed: %v", err)
	}
}
