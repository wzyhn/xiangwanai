package xiangwanapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func newCoverImageTestHandler(t *testing.T) (*CoverImageHandler, string) {
	t.Helper()
	root := t.TempDir()
	store, err := NewLocalCoverImageStore(root)
	if err != nil {
		t.Fatalf("NewLocalCoverImageStore() error = %v", err)
	}
	return &CoverImageHandler{store: store}, root
}

func coverImageMultipart(t *testing.T, payload []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	// Deliberately claim a different part Content-Type than the payload: the
	// handler must trust magic bytes only.
	part, err := writer.CreateFormFile("image", "cover.bin")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("part.Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}
	return body, writer.FormDataContentType()
}

func coverImageUploadContext(t *testing.T, body *bytes.Buffer, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/cover-images", body)
	request.Header.Set("Content-Type", contentType)
	context.Request = request
	context.Set(adminPrincipalContextKey, xiangwanAdminPrincipalForTest())
	return context, recorder
}

func TestCoverImageUploadRejectsNonImageBytes(t *testing.T) {
	t.Parallel()

	handler, _ := newCoverImageTestHandler(t)
	body, contentType := coverImageMultipart(t, []byte("definitely not an image"))
	context, recorder := coverImageUploadContext(t, body, contentType)
	handler.Upload(context)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("Upload() status = %d, want 400", recorder.Code)
	}
}

func TestCoverImageUploadRejectsGifMagicBytes(t *testing.T) {
	t.Parallel()

	handler, _ := newCoverImageTestHandler(t)
	gif := append([]byte("GIF89a"), make([]byte, 64)...)
	body, contentType := coverImageMultipart(t, gif)
	context, recorder := coverImageUploadContext(t, body, contentType)
	handler.Upload(context)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("Upload() status = %d, want 400 for GIF", recorder.Code)
	}
}

func TestCoverImageUploadRequiresAuthorizerAndDatabase(t *testing.T) {
	t.Parallel()

	handler, _ := newCoverImageTestHandler(t)
	payload := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3}
	body, contentType := coverImageMultipart(t, payload)
	context, recorder := coverImageUploadContext(t, body, contentType)
	handler.Upload(context)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("Upload() status = %d, want 500 without authorizer", recorder.Code)
	}
}

func TestCoverImagePathMatchesStoredName(t *testing.T) {
	t.Parallel()

	name := "0123456789abcdef0123456789abcdef.webp"
	if got := CoverImagePath(name); got != "/api/v1/xiangwan/covers/"+name {
		t.Fatalf("CoverImagePath() = %q", got)
	}
	if !strings.HasPrefix(CoverImagePath(name), "/api/v1/xiangwan/covers/") {
		t.Fatalf("CoverImagePath() = %q, want /api/v1/xiangwan/covers prefix", CoverImagePath(name))
	}
}

func TestLocalCoverImageStoreSaveAndOpenRoundTrip(t *testing.T) {
	t.Parallel()

	handler, _ := newCoverImageTestHandler(t)
	payload := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3}
	name, err := handler.store.Save(payload, ".png")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !strings.HasSuffix(name, ".png") || len(name) != 32+4 {
		t.Fatalf("Save() name = %q", name)
	}
	object, err := handler.store.Open(name)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = object.Close() }()
	loaded, err := io.ReadAll(object)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Equal(loaded, payload) {
		t.Fatalf("round trip = %v, want %v", loaded, payload)
	}
}

func TestLocalCoverImageStoreRejectsTraversalNames(t *testing.T) {
	t.Parallel()

	handler, root := newCoverImageTestHandler(t)
	// Plant a file outside the store root that a traversal would reach.
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatalf("plant outside file: %v", err)
	}
	defer func() { _ = os.Remove(outside) }()
	for _, name := range []string{
		"../secret.txt",
		"..\\secret.txt",
		"0123456789abcdef0123456789abcdef.png/../secret.txt",
		"0123456789abcdef0123456789abcdef.gif",
		"0123456789ABCDEF0123456789ABCDEF.png",
		"short.png",
	} {
		if object, err := handler.store.Open(name); err == nil {
			_ = object.Close()
			t.Fatalf("Open(%q) succeeded, want rejection", name)
		}
	}
}

func TestLocalCoverImageStoreListAndRemove(t *testing.T) {
	t.Parallel()

	handler, root := newCoverImageTestHandler(t)
	payload := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3}
	name, err := handler.store.Save(payload, ".png")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	local, ok := handler.store.(*LocalCoverImageStore)
	if !ok {
		t.Fatalf("store = %T, want *LocalCoverImageStore", handler.store)
	}
	files, err := local.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(files) != 1 || files[0].Name != name || files[0].ModTime.IsZero() {
		t.Fatalf("List() = %+v, want one entry for %s", files, name)
	}
	if err := local.Remove(name); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
		t.Fatalf("removed cover still present: %v", err)
	}
	if err := local.Remove("../secret.png"); err == nil {
		t.Fatal("Remove() accepted a traversal name")
	}
	if err := local.Remove("0123456789abcdef0123456789abcdef.gif"); err == nil {
		t.Fatal("Remove() accepted a non-emitted extension")
	}
}

// fakeCoverReferenceChecker records probed filenames and replays canned verdicts.
type fakeCoverReferenceChecker struct {
	referenced map[string]bool
	err        error
	probes     []string
}

func (fake *fakeCoverReferenceChecker) Referenced(
	_ context.Context,
	filename string,
) (bool, error) {
	fake.probes = append(fake.probes, filename)
	if fake.err != nil {
		return false, fake.err
	}
	return fake.referenced[filename], nil
}

// fakeCoverGCMarkerStore replays canned orphan timestamps and records writes.
type fakeCoverGCMarkerStore struct {
	orphanedAt map[string]time.Time
	deleted    map[string]bool
	marked     []string
	cleared    []string
	err        error
}

func (fake *fakeCoverGCMarkerStore) IsDeleted(
	_ context.Context,
	filename string,
) (bool, error) {
	if fake.err != nil {
		return false, fake.err
	}
	return fake.deleted[filename], nil
}

func (fake *fakeCoverGCMarkerStore) OrphanedAt(
	_ context.Context,
	filename string,
) (time.Time, bool, error) {
	if fake.err != nil {
		return time.Time{}, false, fake.err
	}
	at, ok := fake.orphanedAt[filename]
	return at, ok, nil
}

func (fake *fakeCoverGCMarkerStore) MarkOrphaned(
	_ context.Context,
	filename string,
) error {
	if fake.err != nil {
		return fake.err
	}
	if fake.orphanedAt == nil {
		fake.orphanedAt = map[string]time.Time{}
	}
	if _, ok := fake.orphanedAt[filename]; !ok {
		fake.orphanedAt[filename] = time.Now()
	}
	fake.marked = append(fake.marked, filename)
	return nil
}

func (fake *fakeCoverGCMarkerStore) Clear(
	_ context.Context,
	filename string) error {
	if fake.err != nil {
		return fake.err
	}
	delete(fake.orphanedAt, filename)
	fake.cleared = append(fake.cleared, filename)
	return nil
}

func (fake *fakeCoverGCMarkerStore) MarkDeleted(
	_ context.Context,
	filename string,
) error {
	if fake.err != nil {
		return fake.err
	}
	if fake.deleted == nil {
		fake.deleted = map[string]bool{}
	}
	fake.deleted[filename] = true
	return nil
}

func gcTestContext(t *testing.T) *gin.Context {
	t.Helper()
	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/cover-images", nil)
	return context
}

// plantStaleCover stores one cover and backdates it past the upload-age
// cutoff so the GC probes it.
func plantStaleCover(t *testing.T, local *LocalCoverImageStore, payload string) (string, string) {
	t.Helper()
	name, err := local.Save([]byte(payload), ".png")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	oldTime := time.Now().Add(-orphanCoverImageMaxAge - time.Hour)
	if err := os.Chtimes(filepath.Join(local.root, name), oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}
	return name, local.root
}

func TestGcOrphanCoversTwoPhaseMarkThenCollect(t *testing.T) {
	t.Parallel()

	handler, root := newCoverImageTestHandler(t)
	local := handler.store.(*LocalCoverImageStore)
	staleUnreferenced, _ := plantStaleCover(t, local, "stale-a")
	staleReferenced, _ := plantStaleCover(t, local, "stale-b")
	freshUnreferenced, err := local.Save([]byte("fresh"), ".webp")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	checker := &fakeCoverReferenceChecker{
		referenced: map[string]bool{staleReferenced: true},
	}
	markers := &fakeCoverGCMarkerStore{}
	handler.coverRefs = checker
	handler.gcMarkers = markers

	// Pass one: nothing is deleted. The unreferenced stale cover is marked
	// with the first observation; the referenced and fresh ones are kept.
	handler.gcOrphanCovers(gcTestContext(t))
	if _, err := os.Stat(filepath.Join(root, staleUnreferenced)); err != nil {
		t.Fatalf("freshly observed orphan must survive the retention window: %v", err)
	}
	if _, ok := markers.orphanedAt[staleUnreferenced]; !ok {
		t.Fatal("unreferenced stale cover was not marked")
	}
	if _, err := os.Stat(filepath.Join(root, staleReferenced)); err != nil {
		t.Fatalf("referenced cover must be kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, freshUnreferenced)); err != nil {
		t.Fatalf("fresh cover must be kept: %v", err)
	}

	// Pass two: a marker aged past the retention window collects the object and
	// leaves a durable deletion tombstone.
	markers.orphanedAt[staleUnreferenced] =
		time.Now().Add(-orphanCoverRetention - time.Hour)
	handler.gcOrphanCovers(gcTestContext(t))
	if _, err := os.Stat(filepath.Join(root, staleUnreferenced)); !os.IsNotExist(err) {
		t.Fatalf("orphan past retention still present: %v", err)
	}
	if !markers.deleted[staleUnreferenced] {
		t.Fatal("collected cover did not keep its deletion tombstone")
	}
	if _, err := os.Stat(filepath.Join(root, staleReferenced)); err != nil {
		t.Fatalf("referenced cover must be kept: %v", err)
	}
	if len(checker.probes) != 4 {
		t.Fatalf("reference probes = %v, want the two stale covers per pass",
			checker.probes)
	}
}

func TestGcOrphanCoversKeepsOrphanInsideRetentionWindow(t *testing.T) {
	t.Parallel()

	handler, root := newCoverImageTestHandler(t)
	local := handler.store.(*LocalCoverImageStore)
	stale, _ := plantStaleCover(t, local, "stale")
	markers := &fakeCoverGCMarkerStore{orphanedAt: map[string]time.Time{
		stale: time.Now().Add(-orphanCoverRetention + time.Hour),
	}}
	handler.coverRefs = &fakeCoverReferenceChecker{referenced: map[string]bool{}}
	handler.gcMarkers = markers

	handler.gcOrphanCovers(gcTestContext(t))
	if _, err := os.Stat(filepath.Join(root, stale)); err != nil {
		t.Fatalf("orphan inside the retention window must be kept: %v", err)
	}
	if _, ok := markers.orphanedAt[stale]; !ok {
		t.Fatal("retention window must not clear the marker")
	}
}

func TestGcOrphanCoversClearsMarkerWhenCoverIsReferencedAgain(t *testing.T) {
	t.Parallel()

	handler, root := newCoverImageTestHandler(t)
	local := handler.store.(*LocalCoverImageStore)
	stale, _ := plantStaleCover(t, local, "stale")
	markers := &fakeCoverGCMarkerStore{orphanedAt: map[string]time.Time{
		// Even an aged marker must not delete a cover that is referenced
		// again; the marker itself is cleared so a later replacement starts
		// a fresh retention window.
		stale: time.Now().Add(-orphanCoverRetention - time.Hour),
	}}
	handler.coverRefs = &fakeCoverReferenceChecker{referenced: map[string]bool{stale: true}}
	handler.gcMarkers = markers

	handler.gcOrphanCovers(gcTestContext(t))
	if _, err := os.Stat(filepath.Join(root, stale)); err != nil {
		t.Fatalf("re-referenced cover must be kept: %v", err)
	}
	if _, ok := markers.orphanedAt[stale]; ok {
		t.Fatal("re-referenced cover kept its orphan marker")
	}
	if len(markers.cleared) != 1 || markers.cleared[0] != stale {
		t.Fatalf("cleared markers = %v, want %s", markers.cleared, stale)
	}
}

func TestGcOrphanCoversWithoutMarkerStoreDeletesNothing(t *testing.T) {
	t.Parallel()

	handler, root := newCoverImageTestHandler(t)
	local := handler.store.(*LocalCoverImageStore)
	stale, _ := plantStaleCover(t, local, "stale")
	handler.coverRefs = &fakeCoverReferenceChecker{referenced: map[string]bool{}}
	handler.gcMarkers = nil

	handler.gcOrphanCovers(gcTestContext(t))
	if _, err := os.Stat(filepath.Join(root, stale)); err != nil {
		t.Fatalf("cover deleted without a marker store: %v", err)
	}
}

func TestGcOrphanCoversToleratesProbeAndRemoveFailures(t *testing.T) {
	t.Parallel()

	handler, root := newCoverImageTestHandler(t)
	local := handler.store.(*LocalCoverImageStore)
	stale, _ := plantStaleCover(t, local, "stale")

	// Probe failures keep the object and never fail the request.
	failing := &fakeCoverReferenceChecker{err: errors.New("database is gone")}
	handler.coverRefs = failing
	handler.gcMarkers = &fakeCoverGCMarkerStore{}
	context := gcTestContext(t)
	handler.gcOrphanCovers(context)
	if _, err := os.Stat(filepath.Join(root, stale)); err != nil {
		t.Fatalf("cover removed despite probe failure: %v", err)
	}
	if len(context.Errors) == 0 {
		t.Fatal("probe failure was not logged into the request")
	}

	// Marker failures keep the object and only log.
	handler.coverRefs = &fakeCoverReferenceChecker{referenced: map[string]bool{}}
	handler.gcMarkers = &fakeCoverGCMarkerStore{err: errors.New("database is gone")}
	context = gcTestContext(t)
	handler.gcOrphanCovers(context)
	if _, err := os.Stat(filepath.Join(root, stale)); err != nil {
		t.Fatalf("cover removed despite marker failure: %v", err)
	}
	if len(context.Errors) == 0 {
		t.Fatal("marker failure was not logged into the request")
	}

	// A store whose Remove fails keeps the object and only logs.
	handler.store = &failingRemoveCoverStore{LocalCoverImageStore: local}
	handler.gcMarkers = &fakeCoverGCMarkerStore{orphanedAt: map[string]time.Time{
		stale: time.Now().Add(-orphanCoverRetention - time.Hour),
	}}
	context = gcTestContext(t)
	handler.gcOrphanCovers(context)
	if _, err := os.Stat(filepath.Join(root, stale)); err != nil {
		t.Fatalf("cover removed despite Remove failure: %v", err)
	}
	if len(context.Errors) == 0 {
		t.Fatal("Remove failure was not logged into the request")
	}
}

// failingRemoveCoverStore fails every Remove to exercise GC error tolerance.
type failingRemoveCoverStore struct {
	*LocalCoverImageStore
}

func (store *failingRemoveCoverStore) Remove(name string) error {
	return errors.New("disk is read-only")
}

func TestCoverImageUploadAcceptsFileAtExactSizeLimit(t *testing.T) {
	t.Parallel()

	// The multipart framing must not trip the body reader: a file of exactly
	// the cap plus its part headers stays under the reader slack and passes
	// the per-file size check.
	payload := append(
		[]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
		make([]byte, maxCoverImageBytes-8)...,
	)
	handler, root := newCoverImageTestHandler(t)
	body, contentType := coverImageMultipart(t, payload)
	context, recorder := coverImageUploadContext(t, body, contentType)
	handler.Upload(context)
	// Without an authorizer the upload stops at the availability check; the
	// framing must not have failed it earlier with 400.
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("Upload(exact size) status = %d body=%s, want 500 (no authorizer)",
			recorder.Code, recorder.Body.String())
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("no cover may be stored without authorization: %d entries", len(entries))
	}
}

func TestCoverImageGetServesStoredImageWithImmutableCache(t *testing.T) {
	t.Parallel()

	handler, _ := newCoverImageTestHandler(t)
	payload := append(
		[]byte("RIFF\x00\x00\x00\x00WEBP"), make([]byte, 32)...,
	)
	name, err := handler.store.Save(payload, ".webp")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(
		http.MethodGet, "/covers/"+name, nil,
	)
	context.Request = request
	context.Params = gin.Params{{Key: "filename", Value: name}}
	handler.Get(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("Get() status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/webp" {
		t.Fatalf("Content-Type = %q, want image/webp", got)
	}
	if got := recorder.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("Cache-Control = %q, want immutable", got)
	}
	if !bytes.Equal(recorder.Body.Bytes(), payload) {
		t.Fatalf("served %d bytes, want %d", recorder.Body.Len(), len(payload))
	}
}
