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
	maxBrandHeroImageBytes   = 5 << 20
	maxBrandHeroUploadBytes  = maxBrandHeroImageBytes + (1 << 20)
	orphanBrandHeroMaxAge    = 24 * time.Hour
	orphanBrandHeroRetention = 7 * 24 * time.Hour
)

// brandHeroFilePattern pins every public hero filename this handler ever
// emits: 32 lowercase hex characters plus one of the three accepted
// extensions. The pattern is the path-traversal guard for the public read.
var brandHeroFilePattern = regexp.MustCompile(`^[0-9a-f]{32}\.(jpg|png|webp)$`)

var brandHeroExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// BrandHeroStore persists the immutable banner images behind the public
// /brand-hero/:filename route.
type BrandHeroStore interface {
	Save(data []byte, extension string) (string, error)
	Open(name string) (*os.File, error)
	List() ([]StoredBrandHero, error)
	Remove(name string) error
}

type StoredBrandHero struct {
	Name    string
	ModTime time.Time
}

// LocalBrandHeroStore writes each upload into one dedicated directory under
// the runtime's local storage root. Filenames are generated server-side, so
// a stored object is immutable and safe to cache forever.
type LocalBrandHeroStore struct {
	root string
}

func NewLocalBrandHeroStore(root string) (*LocalBrandHeroStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("brand hero storage root is empty")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create brand hero storage: %w", err)
	}
	return &LocalBrandHeroStore{root: root}, nil
}

func (store *LocalBrandHeroStore) Save(data []byte, extension string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate brand hero name: %w", err)
	}
	name := hex.EncodeToString(raw) + extension
	path := filepath.Join(store.root, name)
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return "", fmt.Errorf("write brand hero image: %w", err)
	}
	return name, nil
}

func (store *LocalBrandHeroStore) Open(name string) (*os.File, error) {
	if !brandHeroFilePattern.MatchString(name) {
		return nil, os.ErrNotExist
	}
	return os.Open(filepath.Join(store.root, name))
}

func (store *LocalBrandHeroStore) List() ([]StoredBrandHero, error) {
	if store == nil || strings.TrimSpace(store.root) == "" {
		return nil, errors.New("brand hero storage is unavailable")
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return nil, err
	}
	files := make([]StoredBrandHero, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !brandHeroFilePattern.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		files = append(files, StoredBrandHero{Name: entry.Name(), ModTime: info.ModTime()})
	}
	return files, nil
}

func (store *LocalBrandHeroStore) Remove(name string) error {
	if store == nil || !brandHeroFilePattern.MatchString(name) {
		return os.ErrNotExist
	}
	return os.Remove(filepath.Join(store.root, name))
}

type BrandHeroReferenceChecker interface {
	Referenced(context.Context, string) (bool, error)
}

type BrandHeroReferenceLocker interface {
	WithBrandHeroLock(context.Context, string, func() error) error
}

type BrandHeroGCMarkerStore interface {
	OrphanedAt(context.Context, string) (time.Time, bool, error)
	IsDeleted(context.Context, string) (bool, error)
	MarkOrphaned(context.Context, string) error
	Clear(context.Context, string) error
	MarkDeleted(context.Context, string) error
}

type brandHeroReferenceAdapter struct {
	querier  *adminpostgres.BrandHeroReferenceQuerier
	database adminpostgres.RowQueryer
}

func NewBrandHeroReferenceAdapter(
	querier *adminpostgres.BrandHeroReferenceQuerier,
	database adminpostgres.RowQueryer,
) BrandHeroReferenceChecker {
	return brandHeroReferenceAdapter{querier: querier, database: database}
}

func (adapter brandHeroReferenceAdapter) Referenced(
	ctx context.Context, filename string,
) (bool, error) {
	return adapter.querier.Referenced(ctx, adapter.database, filename)
}

func (adapter brandHeroReferenceAdapter) WithBrandHeroLock(
	ctx context.Context, filename string, fn func() error,
) error {
	if adapter.database == nil || fn == nil || !brandHeroFilePattern.MatchString(filename) {
		return errors.New("invalid brand hero reference lock")
	}
	beginner, ok := adapter.database.(interface {
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	})
	if !ok {
		return fn()
	}
	tx, err := beginner.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin brand hero reference lock: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
SELECT pg_advisory_xact_lock(hashtextextended($1, 0))
`, filename); err != nil {
		return fmt.Errorf("lock brand hero reference: %w", err)
	}
	if err := fn(); err != nil {
		return err
	}
	return tx.Commit()
}

type brandHeroGCMarkerAdapter struct {
	store    *adminpostgres.BrandHeroGCMarkerStore
	database adminpostgres.CoverGCMarkerDB
}

func NewBrandHeroGCMarkerAdapter(
	store *adminpostgres.BrandHeroGCMarkerStore,
	database adminpostgres.CoverGCMarkerDB,
) BrandHeroGCMarkerStore {
	return brandHeroGCMarkerAdapter{store: store, database: database}
}

func (adapter brandHeroGCMarkerAdapter) OrphanedAt(ctx context.Context, filename string) (time.Time, bool, error) {
	return adapter.store.OrphanedAt(ctx, adapter.database, filename)
}
func (adapter brandHeroGCMarkerAdapter) IsDeleted(ctx context.Context, filename string) (bool, error) {
	return adapter.store.IsDeleted(ctx, adapter.database, filename)
}
func (adapter brandHeroGCMarkerAdapter) MarkOrphaned(ctx context.Context, filename string) error {
	return adapter.store.MarkOrphaned(ctx, adapter.database, filename)
}
func (adapter brandHeroGCMarkerAdapter) Clear(ctx context.Context, filename string) error {
	return adapter.store.Clear(ctx, adapter.database, filename)
}
func (adapter brandHeroGCMarkerAdapter) MarkDeleted(ctx context.Context, filename string) error {
	return adapter.store.MarkDeleted(ctx, adapter.database, filename)
}

type BrandHeroHandler struct {
	store      BrandHeroStore
	authorizer *adminpostgres.GrantAuthorizer
	database   adminpostgres.RowQueryer
	refs       BrandHeroReferenceChecker
	markers    BrandHeroGCMarkerStore
	gcMu       sync.Mutex
	gcRunning  bool
}

func (handler *BrandHeroHandler) SetGCDependencies(
	refs BrandHeroReferenceChecker,
	markers BrandHeroGCMarkerStore,
) {
	if handler == nil {
		return
	}
	handler.refs = refs
	handler.markers = markers
}

func NewBrandHeroHandler(
	store BrandHeroStore,
	authorizer *adminpostgres.GrantAuthorizer,
	database adminpostgres.RowQueryer,
) *BrandHeroHandler {
	return &BrandHeroHandler{store: store, authorizer: authorizer, database: database}
}

func (handler *BrandHeroHandler) RegisterAdminRoutes(group *gin.RouterGroup) {
	group.POST("/brand-hero-images", handler.Upload)
}

func (handler *BrandHeroHandler) RegisterPublicRoutes(group *gin.RouterGroup) {
	group.GET("/brand-hero/:filename", handler.Get)
	group.HEAD("/brand-hero/:filename", handler.Get)
}

// Upload godoc
// @Summary Upload one Xiangwan brand banner image
// @Description Stores one JPEG/PNG/WebP banner (max 5 MiB) for the brand profile hero and returns its public relative URL. Unreferenced uploads are collected asynchronously after a seven-day orphan window.
// @Tags xiangwan-admin
// @Accept multipart/form-data
// @Produce json
// @Param image formData file true "Banner image"
// @Success 200 {object} response.Body
// @Failure 400 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/admin/brand-hero-images [post]
func (handler *BrandHeroHandler) Upload(c *gin.Context) {
	if handler == nil || handler.store == nil {
		writeError(c, errx.NewInternal("brand hero upload is unavailable"))
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
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBrandHeroUploadBytes)
	fileHeader, fileErr := c.FormFile("image")
	if fileErr != nil {
		writeError(c, errx.NewBadRequest("invalid administrator request"))
		return
	}
	if fileHeader.Size > maxBrandHeroImageBytes {
		writeError(c, errx.NewBadRequest("banner image is too large"))
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
	extension, accepted := brandHeroExtensions[detectImageContentType(data)]
	if !accepted {
		writeError(c, errx.NewBadRequest("banner image must be JPEG, PNG, or WebP"))
		return
	}
	if handler.authorizer == nil || handler.database == nil {
		writeError(c, errx.NewInternal("brand hero upload is unavailable"))
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
		writeError(c, errx.NewInternal("brand hero upload failed"))
		return
	}
	handler.startOrphanGC(logx.Detach(c.Request.Context()))
	response.OK(c, gin.H{"url": BrandHeroPath(name)})
}

func (handler *BrandHeroHandler) startOrphanGC(parent context.Context) {
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
		handler.gcOrphanHeroes(ctx, func(err error) {
			logx.FromContext(ctx).Error("xiangwan brand hero orphan GC failed", zap.Error(err))
		})
	}()
}

func (handler *BrandHeroHandler) gcOrphanHeroes(
	ctx context.Context,
	report func(error),
) {
	if handler == nil || handler.store == nil || handler.refs == nil || handler.markers == nil {
		return
	}
	files, err := handler.store.List()
	if err != nil {
		if report != nil {
			report(fmt.Errorf("list brand heroes for orphan GC: %w", err))
		}
		return
	}
	cutoff := time.Now().Add(-orphanBrandHeroMaxAge)
	for _, file := range files {
		if !brandHeroFilePattern.MatchString(file.Name) || !file.ModTime.Before(cutoff) {
			continue
		}
		process := func() error {
			referenced, err := handler.refs.Referenced(ctx, file.Name)
			if err != nil {
				return fmt.Errorf("probe brand hero reference %s: %w", file.Name, err)
			}
			if referenced {
				return handler.markers.Clear(ctx, file.Name)
			}
			deleted, err := handler.markers.IsDeleted(ctx, file.Name)
			if err != nil {
				return fmt.Errorf("read brand hero deletion state %s: %w", file.Name, err)
			}
			if deleted {
				if err := handler.store.Remove(file.Name); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove tombstoned brand hero %s: %w", file.Name, err)
				}
				return nil
			}
			orphanedAt, marked, err := handler.markers.OrphanedAt(ctx, file.Name)
			if err != nil {
				return fmt.Errorf("read brand hero GC marker %s: %w", file.Name, err)
			}
			if !marked {
				return handler.markers.MarkOrphaned(ctx, file.Name)
			}
			if !orphanedAt.Before(time.Now().Add(-orphanBrandHeroRetention)) {
				return nil
			}
			if err := handler.markers.MarkDeleted(ctx, file.Name); err != nil {
				return err
			}
			if err := handler.store.Remove(file.Name); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove orphan brand hero %s: %w", file.Name, err)
			}
			return nil
		}
		var processErr error
		if locker, ok := handler.refs.(BrandHeroReferenceLocker); ok {
			processErr = locker.WithBrandHeroLock(ctx, file.Name, process)
		} else {
			processErr = process()
		}
		if processErr != nil && report != nil {
			report(processErr)
		}
	}
}

// Get godoc
// @Summary Read one Xiangwan brand banner image
// @Description Anonymous immutable read of one uploaded banner; filenames are server-generated and never reused.
// @Tags xiangwan
// @Produce application/octet-stream
// @Param filename path string true "Server-generated banner filename"
// @Success 200 {file} binary
// @Failure 404 {object} response.Body
// @Router /xiangwan/brand-hero/{filename} [get]
func (handler *BrandHeroHandler) Get(c *gin.Context) {
	if handler == nil || handler.store == nil {
		writeError(c, errx.NewInternal("brand hero media is unavailable"))
		return
	}
	object, err := handler.store.Open(c.Param("filename"))
	if err != nil {
		writeError(c, errx.NewNotFound("brand hero image not found"))
		return
	}
	defer func() { _ = object.Close() }()
	c.Header("Content-Type", brandHeroContentType(c.Param("filename")))
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(c.Writer, c.Request, c.Param("filename"), time.Time{}, object)
}

// BrandHeroPath returns the public relative path of one stored banner.
func BrandHeroPath(name string) string {
	return "/api/v1/xiangwan/brand-hero/" + name
}

// detectImageContentType sniffs magic bytes: the multipart part's own
// Content-Type is never trusted, and net/http's sniffer does not know WebP.
func detectImageContentType(data []byte) string {
	switch {
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg"
	case len(data) >= 8 && string(data[0:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	default:
		return ""
	}
}

func brandHeroContentType(name string) string {
	for mediaType, extension := range brandHeroExtensions {
		if extension == strings.ToLower(filepath.Ext(name)) {
			return mediaType
		}
	}
	return "application/octet-stream"
}
