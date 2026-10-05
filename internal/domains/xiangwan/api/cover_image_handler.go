package xiangwanapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	adminpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/logx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	maxCoverImageBytes       = 5 << 20
	maxCoverImageUploadBytes = maxCoverImageBytes + (1 << 20)
	// orphanCoverImageMaxAge keeps the GC away from covers uploaded in the
	// last 24h: an operator may upload now and attach the cover to an
	// Instance minutes later, so young files are not even probed for
	// references.
	orphanCoverImageMaxAge = 24 * time.Hour
	// orphanCoverRetention is the second GC phase, counted from the first
	// observation that a cover lost every Instance reference (not from its
	// upload time): the public read stamps each cover with a one-year
	// immutable Cache-Control header, so a client can keep serving an old
	// cover URL for up to a year after the Instance moved to a new cover. A
	// just-replaced cover must therefore survive at least a grace window
	// after losing its reference. The window is deliberately far shorter
	// than the cache ceiling — realistic clients refetch the Instance detail
	// after a cover change — but long enough for a stale browser/CDN copy to
	// still resolve during the grace period.
	orphanCoverRetention = 7 * 24 * time.Hour
)

// coverImageFilePattern pins every public cover filename this handler ever
// emits: 32 lowercase hex characters plus one of the three accepted
// extensions. The pattern is the path-traversal guard for the public read.
var coverImageFilePattern = regexp.MustCompile(`^[0-9a-f]{32}\.(jpg|png|webp)$`)

var coverImageExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// CoverImageStore persists the immutable cover images behind the public
// /covers/:filename route.
type CoverImageStore interface {
	Save(data []byte, extension string) (string, error)
	Open(name string) (*os.File, error)
}

// StoredCoverImage is one on-disk cover object observed by the orphan GC.
type StoredCoverImage struct {
	Name    string
	ModTime time.Time
}

// CoverImageGCStore is the store surface the upload-time orphan GC needs:
// listing on-disk objects with their modification time and removing the
// unreferenced ones.
type CoverImageGCStore interface {
	CoverImageStore
	List() ([]StoredCoverImage, error)
	Remove(name string) error
}

// CoverReferenceChecker reports whether one stored cover filename is still
// referenced by any activity Instance. The upload-time GC depends on this
// seam so tests can substitute a fake.
type CoverReferenceChecker interface {
	Referenced(ctx context.Context, filename string) (bool, error)
}

type CoverReferenceLocker interface {
	WithCoverLock(ctx context.Context, filename string, fn func() error) error
}

// CoverGCMarkerStore records when a cover object first lost every Instance
// reference. The two-phase orphan GC inserts a marker on first observation
// instead of deleting, and only removes the object once the marker is older
// than the retention window — so a long-referenced cover that was just
// replaced cannot be deleted while clients still hold its immutable cached
// URL.
type CoverGCMarkerStore interface {
	// OrphanedAt reports when the filename was first observed unreferenced.
	OrphanedAt(ctx context.Context, filename string) (time.Time, bool, error)
	IsDeleted(ctx context.Context, filename string) (bool, error)
	// MarkOrphaned records the first unreferenced observation; an already
	// marked filename keeps its original timestamp.
	MarkOrphaned(ctx context.Context, filename string) error
	// Clear drops the marker after the file is deleted or becomes
	// referenced again.
	Clear(ctx context.Context, filename string) error
	MarkDeleted(ctx context.Context, filename string) error
}

// LocalCoverImageStore writes each upload into one dedicated directory under
// the runtime's local storage root. Filenames are generated server-side, so
// a stored object is immutable and safe to cache forever.
type LocalCoverImageStore struct {
	root string
}

func NewLocalCoverImageStore(root string) (*LocalCoverImageStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("cover image storage root is empty")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create cover image storage: %w", err)
	}
	return &LocalCoverImageStore{root: root}, nil
}

func (store *LocalCoverImageStore) Save(data []byte, extension string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate cover image name: %w", err)
	}
	name := hex.EncodeToString(raw) + extension
	path := filepath.Join(store.root, name)
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return "", fmt.Errorf("write cover image: %w", err)
	}
	return name, nil
}

func (store *LocalCoverImageStore) Open(name string) (*os.File, error) {
	if !coverImageFilePattern.MatchString(name) {
		return nil, os.ErrNotExist
	}
	return os.Open(filepath.Join(store.root, name))
}

// List returns every regular file in the storage root with its modification
// time; the GC filters by the emitted filename pattern and age itself.
func (store *LocalCoverImageStore) List() ([]StoredCoverImage, error) {
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return nil, fmt.Errorf("list cover image storage: %w", err)
	}
	files := make([]StoredCoverImage, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, StoredCoverImage{
			Name:    entry.Name(),
			ModTime: info.ModTime(),
		})
	}
	return files, nil
}

func (store *LocalCoverImageStore) Remove(name string) error {
	if !coverImageFilePattern.MatchString(name) {
		return os.ErrNotExist
	}
	return os.Remove(filepath.Join(store.root, name))
}

// coverReferenceAdapter binds the tenant-scoped querier to the runtime
// database so the handler seam stays a one-argument probe.
type coverReferenceAdapter struct {
	querier  *adminpostgres.CoverImageReferenceQuerier
	database adminpostgres.RowQueryer
}

// NewCoverReferenceAdapter adapts the admin cover reference querier to the
// handler's CoverReferenceChecker seam.
func NewCoverReferenceAdapter(
	querier *adminpostgres.CoverImageReferenceQuerier,
	database adminpostgres.RowQueryer,
) CoverReferenceChecker {
	return coverReferenceAdapter{querier: querier, database: database}
}

func (adapter coverReferenceAdapter) Referenced(
	ctx context.Context,
	filename string,
) (bool, error) {
	return adapter.querier.Referenced(ctx, adapter.database, filename)
}

func (adapter coverReferenceAdapter) WithCoverLock(
	ctx context.Context,
	filename string,
	fn func() error,
) error {
	if adapter.database == nil || fn == nil || !coverImageFilePattern.MatchString(filename) {
		return errors.New("invalid cover reference lock")
	}
	beginner, ok := adapter.database.(interface {
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	})
	if !ok {
		return fn()
	}
	tx, err := beginner.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin cover reference lock: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
SELECT pg_advisory_xact_lock(hashtextextended($1, 0))
`, filename); err != nil {
		return fmt.Errorf("lock cover reference: %w", err)
	}
	if err := fn(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit cover reference lock: %w", err)
	}
	return nil
}

// coverGCMarkerAdapter binds the marker store to the runtime database so the
// handler seam stays filename-only.
type coverGCMarkerAdapter struct {
	store    *adminpostgres.CoverGCMarkerStore
	database adminpostgres.CoverGCMarkerDB
}

// NewCoverGCMarkerAdapter adapts the admin cover GC marker store to the
// handler's CoverGCMarkerStore seam.
func NewCoverGCMarkerAdapter(
	store *adminpostgres.CoverGCMarkerStore,
	database adminpostgres.CoverGCMarkerDB,
) CoverGCMarkerStore {
	return coverGCMarkerAdapter{store: store, database: database}
}

func (adapter coverGCMarkerAdapter) OrphanedAt(
	ctx context.Context,
	filename string,
) (time.Time, bool, error) {
	return adapter.store.OrphanedAt(ctx, adapter.database, filename)
}

func (adapter coverGCMarkerAdapter) MarkOrphaned(
	ctx context.Context,
	filename string,
) error {
	return adapter.store.MarkOrphaned(ctx, adapter.database, filename)
}

func (adapter coverGCMarkerAdapter) IsDeleted(
	ctx context.Context,
	filename string,
) (bool, error) {
	return adapter.store.IsDeleted(ctx, adapter.database, filename)
}

func (adapter coverGCMarkerAdapter) Clear(
	ctx context.Context,
	filename string,
) error {
	return adapter.store.Clear(ctx, adapter.database, filename)
}

func (adapter coverGCMarkerAdapter) MarkDeleted(
	ctx context.Context,
	filename string,
) error {
	return adapter.store.MarkDeleted(ctx, adapter.database, filename)
}

type CoverImageHandler struct {
	store      CoverImageStore
	authorizer *adminpostgres.GrantAuthorizer
	database   adminpostgres.RowQueryer
	coverRefs  CoverReferenceChecker
	gcMarkers  CoverGCMarkerStore
	gcMu       sync.Mutex
	gcRunning  bool
}

func NewCoverImageHandler(
	store CoverImageStore,
	authorizer *adminpostgres.GrantAuthorizer,
	database adminpostgres.RowQueryer,
	coverRefs CoverReferenceChecker,
	gcMarkers CoverGCMarkerStore,
) *CoverImageHandler {
	return &CoverImageHandler{
		store:      store,
		authorizer: authorizer,
		database:   database,
		coverRefs:  coverRefs,
		gcMarkers:  gcMarkers,
	}
}

func (handler *CoverImageHandler) RegisterAdminRoutes(group *gin.RouterGroup) {
	group.POST("/cover-images", handler.Upload)
}

func (handler *CoverImageHandler) RegisterPublicRoutes(group *gin.RouterGroup) {
	group.GET("/covers/:filename", handler.Get)
	group.HEAD("/covers/:filename", handler.Get)
}

// Upload godoc
// @Summary Upload one Xiangwan activity cover image
// @Description Stores one JPEG/PNG/WebP cover (max 5 MiB) for an activity Instance and returns its public relative URL; the caller stores it as the Instance cover_image_url either as-is (relative path) or composed with the site origin into an absolute https URL. Uploads require an activity-operator Grant. A single background sweep processes the cover directory after a successful upload: objects older than 24h whose filename no Instance references (cover_image_url or detail_blocks) are first marked with the time their reference was lost, and only collected once that mark is older than the 7-day retention window; a re-referenced cover clears its mark. Sweep failures never fail the upload.
// @Tags xiangwan-admin
// @Accept multipart/form-data
// @Produce json
// @Param image formData file true "Cover image"
// @Success 200 {object} response.Body
// @Failure 400 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/admin/cover-images [post]
func (handler *CoverImageHandler) Upload(c *gin.Context) {
	if handler == nil || handler.store == nil {
		writeError(c, errx.NewInternal("cover image upload is unavailable"))
		return
	}
	principal, err := AdminPrincipal(c)
	if err != nil {
		writeAdminError(c, err)
		return
	}
	if c.ContentType() != "multipart/form-data" {
		writeError(c, errx.NewBadRequest("invalid administrator request"))
		return
	}
	// The byte reader caps the whole multipart body, framing overhead
	// included; the per-file size check below stays the real file bound.
	c.Request.Body = http.MaxBytesReader(
		c.Writer, c.Request.Body, maxCoverImageUploadBytes,
	)
	fileHeader, fileErr := c.FormFile("image")
	if fileErr != nil {
		writeError(c, errx.NewBadRequest("invalid administrator request"))
		return
	}
	if fileHeader.Size > maxCoverImageBytes {
		writeError(c, errx.NewBadRequest("cover image is too large"))
		return
	}
	file, openErr := fileHeader.Open()
	if openErr != nil {
		writeError(c, errx.NewBadRequest("invalid administrator request"))
		return
	}
	defer func() { _ = file.Close() }()
	data := make([]byte, fileHeader.Size)
	if _, readErr := io.ReadFull(file, data); readErr != nil {
		writeError(c, errx.NewBadRequest("invalid administrator request"))
		return
	}
	extension, accepted := coverImageExtensions[detectImageContentType(data)]
	if !accepted {
		writeError(c, errx.NewBadRequest("cover image must be JPEG, PNG, or WebP"))
		return
	}
	if handler.authorizer == nil || handler.database == nil {
		writeError(c, errx.NewInternal("cover image upload is unavailable"))
		return
	}
	if err := handler.authorizer.RequireActivityOperatorIdentity(
		c.Request.Context(),
		handler.database,
		principal.PrincipalID,
		principal.IdentityLinkID,
	); err != nil {
		writeAdminError(c, err)
		return
	}
	name, saveErr := handler.store.Save(data, extension)
	if saveErr != nil {
		_ = c.Error(saveErr)
		writeError(c, errx.NewInternal("cover image upload failed"))
		return
	}
	handler.startOrphanGC(logx.Detach(c.Request.Context()))
	response.OK(c, gin.H{"url": CoverImagePath(name)})
}

func (handler *CoverImageHandler) startOrphanGC(parent context.Context) {
	handler.gcMu.Lock()
	if handler.gcRunning {
		handler.gcMu.Unlock()
		return
	}
	handler.gcRunning = true
	handler.gcMu.Unlock()
	go func() {
		defer func() {
			handler.gcMu.Lock()
			handler.gcRunning = false
			handler.gcMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		handler.gcOrphanCoversContext(ctx, func(err error) {
			logx.FromContext(ctx).Error("xiangwan cover orphan GC failed", zap.Error(err))
		})
	}()
}

// gcOrphanCovers sweeps stored cover objects that no Instance references any
// more, in two phases so deletion is counted from the reference loss instead
// of the upload time. Phase one marks: a cover older than
// orphanCoverImageMaxAge whose reference probe comes back negative gets a
// marker recording the first observation (files uploaded and attached within
// a day are never even probed). Phase two collects: once a marker is older
// than orphanCoverRetention, the object and its marker are removed. A cover
// that becomes referenced again clears its marker, so a later replacement
// starts a fresh retention window. Without the marker store the GC refuses
// to delete anything: upload age alone cannot tell how long a file has been
// unreferenced. Every failure — listing, probing, marking, removal — is
// reported to the caller and skipped: the GC must never fail the upload.
func (handler *CoverImageHandler) gcOrphanCovers(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	handler.gcOrphanCoversContext(c.Request.Context(), func(err error) { _ = c.Error(err) })
}

func (handler *CoverImageHandler) gcOrphanCoversContext(
	ctx context.Context,
	report func(error),
) {
	if handler.coverRefs == nil {
		return
	}
	gcStore, ok := handler.store.(CoverImageGCStore)
	if !ok {
		return
	}
	files, err := gcStore.List()
	if err != nil {
		if report != nil {
			report(fmt.Errorf("list covers for orphan GC: %w", err))
		}
		return
	}
	cutoff := time.Now().Add(-orphanCoverImageMaxAge)
	for _, file := range files {
		if !coverImageFilePattern.MatchString(file.Name) ||
			!file.ModTime.Before(cutoff) {
			continue
		}
		process := func() error {
			referenced, err := handler.coverRefs.Referenced(ctx, file.Name)
			if err != nil {
				return fmt.Errorf("probe cover reference %s: %w", file.Name, err)
			}
			if referenced {
				if handler.gcMarkers != nil {
					return handler.gcMarkers.Clear(ctx, file.Name)
				}
				return nil
			}
			if handler.gcMarkers == nil {
				return nil
			}
			deleted, err := handler.gcMarkers.IsDeleted(ctx, file.Name)
			if err != nil {
				return fmt.Errorf("read cover deletion state %s: %w", file.Name, err)
			}
			if deleted {
				if err := gcStore.Remove(file.Name); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove tombstoned cover %s: %w", file.Name, err)
				}
				return nil
			}
			orphanedAt, marked, err := handler.gcMarkers.OrphanedAt(ctx, file.Name)
			if err != nil {
				return fmt.Errorf("read cover GC marker %s: %w", file.Name, err)
			}
			if !marked {
				return handler.gcMarkers.MarkOrphaned(ctx, file.Name)
			}
			if !orphanedAt.Before(time.Now().Add(-orphanCoverRetention)) {
				return nil
			}
			if err := handler.gcMarkers.MarkDeleted(ctx, file.Name); err != nil {
				return err
			}
			if err := gcStore.Remove(file.Name); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove orphan cover %s: %w", file.Name, err)
			}
			return nil
		}
		var processErr error
		if locker, ok := handler.coverRefs.(CoverReferenceLocker); ok {
			processErr = locker.WithCoverLock(ctx, file.Name, process)
		} else {
			processErr = process()
		}
		if processErr != nil && report != nil {
			report(processErr)
		}
	}
}

// Get godoc
// @Summary Read one Xiangwan activity cover image
// @Description Anonymous immutable read of one uploaded cover; filenames are server-generated and never reused.
// @Tags xiangwan
// @Produce application/octet-stream
// @Param filename path string true "Server-generated cover filename"
// @Success 200 {file} binary
// @Failure 404 {object} response.Body
// @Router /xiangwan/covers/{filename} [get]
func (handler *CoverImageHandler) Get(c *gin.Context) {
	if handler == nil || handler.store == nil {
		writeError(c, errx.NewInternal("cover image media is unavailable"))
		return
	}
	object, err := handler.store.Open(c.Param("filename"))
	if err != nil {
		writeError(c, errx.NewNotFound("cover image not found"))
		return
	}
	defer func() { _ = object.Close() }()
	c.Header("Content-Type", coverImageContentType(c.Param("filename")))
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(c.Writer, c.Request, c.Param("filename"), time.Time{}, object)
}

// CoverImagePath returns the public relative path of one stored cover. The
// administrator surface stores it as the Instance cover_image_url either
// as-is (relative path) or composed with the site origin into an absolute
// https URL — both shapes pass the migration 787 CHECK constraint.
func CoverImagePath(name string) string {
	return "/api/v1/xiangwan/covers/" + name
}

func coverImageContentType(name string) string {
	for mediaType, extension := range coverImageExtensions {
		if extension == strings.ToLower(filepath.Ext(name)) {
			return mediaType
		}
	}
	return "application/octet-stream"
}
