package xiangwanapi

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type fakeBrandHeroReferenceChecker struct {
	referenced bool
}

func (fake *fakeBrandHeroReferenceChecker) Referenced(context.Context, string) (bool, error) {
	return fake.referenced, nil
}

type fakeBrandHeroGCMarkers struct {
	orphanedAt map[string]time.Time
	deleted    map[string]bool
}

func (fake *fakeBrandHeroGCMarkers) OrphanedAt(_ context.Context, filename string) (time.Time, bool, error) {
	value, ok := fake.orphanedAt[filename]
	return value, ok, nil
}

func (fake *fakeBrandHeroGCMarkers) IsDeleted(_ context.Context, filename string) (bool, error) {
	return fake.deleted[filename], nil
}

func (fake *fakeBrandHeroGCMarkers) MarkOrphaned(_ context.Context, filename string) error {
	if fake.orphanedAt == nil {
		fake.orphanedAt = map[string]time.Time{}
	}
	fake.orphanedAt[filename] = time.Now().UTC()
	return nil
}

func (fake *fakeBrandHeroGCMarkers) Clear(_ context.Context, filename string) error {
	delete(fake.orphanedAt, filename)
	delete(fake.deleted, filename)
	return nil
}

func (fake *fakeBrandHeroGCMarkers) MarkDeleted(_ context.Context, filename string) error {
	if fake.deleted == nil {
		fake.deleted = map[string]bool{}
	}
	fake.deleted[filename] = true
	return nil
}

func newBrandHeroTestHandler(t *testing.T) (*BrandHeroHandler, string) {
	t.Helper()
	root := t.TempDir()
	store, err := NewLocalBrandHeroStore(root)
	if err != nil {
		t.Fatalf("NewLocalBrandHeroStore() error = %v", err)
	}
	return &BrandHeroHandler{store: store}, root
}

func brandHeroMultipart(t *testing.T, contentType string, payload []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	// Deliberately claim a different part Content-Type than the payload: the
	// handler must trust magic bytes only.
	part, err := writer.CreateFormFile("image", "banner.bin")
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

func brandHeroUploadContext(t *testing.T, body *bytes.Buffer, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/brand-hero-images", body)
	request.Header.Set("Content-Type", contentType)
	context.Request = request
	context.Set(adminPrincipalContextKey, xiangwanAdminPrincipalForTest())
	return context, recorder
}

func xiangwanAdminPrincipalForTest() xiangwanadmin.Principal {
	return xiangwanadmin.Principal{
		PrincipalID:    uuid.New(),
		IdentityLinkID: uuid.New(),
	}
}

func TestBrandHeroUploadRejectsNonImageBytes(t *testing.T) {
	t.Parallel()

	handler, _ := newBrandHeroTestHandler(t)
	body, contentType := brandHeroMultipart(t, "image/jpeg", []byte("definitely not an image"))
	context, recorder := brandHeroUploadContext(t, body, contentType)
	handler.Upload(context)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("Upload() status = %d, want 400", recorder.Code)
	}
}

func TestBrandHeroUploadRejectsGifMagicBytes(t *testing.T) {
	t.Parallel()

	handler, _ := newBrandHeroTestHandler(t)
	gif := append([]byte("GIF89a"), make([]byte, 64)...)
	body, contentType := brandHeroMultipart(t, "image/png", gif)
	context, recorder := brandHeroUploadContext(t, body, contentType)
	handler.Upload(context)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("Upload() status = %d, want 400 for GIF", recorder.Code)
	}
}

func TestBrandHeroUploadAcceptsFileAtExactSizeLimit(t *testing.T) {
	t.Parallel()

	handler, root := newBrandHeroTestHandler(t)
	payload := append(
		[]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
		make([]byte, maxBrandHeroImageBytes-8)...,
	)
	body, contentType := brandHeroMultipart(t, "image/png", payload)
	context, recorder := brandHeroUploadContext(t, body, contentType)
	handler.Upload(context)
	// This unit handler deliberately has no authorizer. Reaching the 500
	// availability check proves multipart framing did not reject the exact
	// 5 MiB file earlier with 400.
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("Upload() status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("stored hero entries = %v, err = %v", entries, err)
	}
}

func TestDetectImageContentTypeMagicBytes(t *testing.T) {
	t.Parallel()

	pngHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 13}
	if got := detectImageContentType(pngHeader); got != "image/png" {
		t.Fatalf("detectImageContentType(png) = %q", got)
	}
	if got := detectImageContentType([]byte{0xFF, 0xD8, 0xFF, 0xE0}); got != "image/jpeg" {
		t.Fatalf("detectImageContentType(jpeg) = %q", got)
	}
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBP"), make([]byte, 16)...)
	if got := detectImageContentType(webp); got != "image/webp" {
		t.Fatalf("detectImageContentType(webp) = %q", got)
	}
	if got := detectImageContentType([]byte("plain text")); got != "" {
		t.Fatalf("detectImageContentType(text) = %q, want empty", got)
	}
}

func TestLocalBrandHeroStoreSaveAndOpenRoundTrip(t *testing.T) {
	t.Parallel()

	handler, _ := newBrandHeroTestHandler(t)
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

func TestLocalBrandHeroStoreRejectsTraversalNames(t *testing.T) {
	t.Parallel()

	handler, root := newBrandHeroTestHandler(t)
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

func TestBrandHeroGCMarksBeforeCollectingAndKeepsTombstone(t *testing.T) {
	t.Parallel()

	handler, root := newBrandHeroTestHandler(t)
	name, err := handler.store.Save([]byte("hero"), ".png")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	old := time.Now().Add(-orphanBrandHeroMaxAge - time.Hour)
	if err := os.Chtimes(filepath.Join(root, name), old, old); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}
	markers := &fakeBrandHeroGCMarkers{}
	handler.SetGCDependencies(&fakeBrandHeroReferenceChecker{}, markers)
	handler.gcOrphanHeroes(context.Background(), func(err error) {
		t.Fatalf("gcOrphanHeroes() error = %v", err)
	})
	if _, err := os.Stat(filepath.Join(root, name)); err != nil {
		t.Fatalf("first GC pass removed hero: %v", err)
	}
	markers.orphanedAt[name] = time.Now().Add(-orphanBrandHeroRetention - time.Hour)
	handler.gcOrphanHeroes(context.Background(), func(err error) {
		t.Fatalf("gcOrphanHeroes() second error = %v", err)
	})
	if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
		t.Fatalf("stale hero still present: %v", err)
	}
	if !markers.deleted[name] {
		t.Fatal("collected hero did not keep deletion tombstone")
	}
}

func TestBrandHeroGetServesStoredImageWithImmutableCache(t *testing.T) {
	t.Parallel()

	handler, _ := newBrandHeroTestHandler(t)
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
		http.MethodGet, "/brand-hero/"+name, nil,
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
