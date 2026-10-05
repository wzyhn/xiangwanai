package xiangwanapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wzyhn/xiangwanai/internal/capabilities/storage/publicobject"
	identitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	// maxConsumerAvatarBytes stays at the WeChat img_sec_check upstream media
	// cap: the API rejects images above 1 MiB, so a larger handler bound would
	// only surface as a provider-side refusal after upload.
	maxConsumerAvatarBytes       = 1 << 20
	maxConsumerAvatarUploadBytes = maxConsumerAvatarBytes + (1 << 20)
	maxConsumerNicknameBytes     = 4 * 1024
	maxConsumerNicknameRunes     = 64
	consumerAvatarPublicPrefix   = "/api/v1/xiangwan/avatars/"
	consumerLegacyAvatarPrefix   = consumerAvatarPublicPrefix + "legacy/"
	maxLegacyAvatarBytes         = 2 << 20
)

// Customer-facing moderation outcomes. These two copies are reviewed wording
// and pass the customerError rewrite whitelist verbatim; every other 400 keeps
// the generic "提交内容有误" guard.
const (
	avatarRejectedCustomerMessage              = "头像未通过内容安全审核，请更换图片"
	avatarModerationUnavailableCustomerMessage = "头像审核服务暂不可用，请稍后重试"
)

// consumerAvatarFilePattern pins every public avatar filename this handler
// ever emits: 32 lowercase hex characters plus one of the three accepted
// extensions. The pattern is the path-traversal guard for the public read.
var consumerAvatarFilePattern = regexp.MustCompile(`^[0-9a-f]{32}\.(jpg|png|webp)$`)

var consumerAvatarExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

var consumerLegacyAvatarMIMEs = map[string]bool{
	"image/gif":  true,
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
}

// ConsumerAvatarStore persists the immutable avatar objects behind the public
// /avatars/:filename route.
type ConsumerAvatarStore interface {
	Save(data []byte, extension string) (string, error)
	Open(name string) (*os.File, error)
	Remove(name string) error
}

// ConsumerLegacyAvatarReader reads the platform Auth avatar object referenced
// by one live Principal. The runtime implementation must enforce the current
// principal avatar_file_id binding before opening the confirmed object.
type ConsumerLegacyAvatarReader interface {
	Open(context.Context, uuid.UUID, uuid.UUID) (*publicobject.Object, error)
}

// LocalConsumerAvatarStore writes each upload into one dedicated directory
// under the runtime's local storage root. Filenames are generated
// server-side, so a stored object is immutable and safe to cache forever.
type LocalConsumerAvatarStore struct {
	root string
}

func NewLocalConsumerAvatarStore(root string) (*LocalConsumerAvatarStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("consumer avatar storage root is empty")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create consumer avatar storage: %w", err)
	}
	return &LocalConsumerAvatarStore{root: root}, nil
}

func (store *LocalConsumerAvatarStore) Save(data []byte, extension string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate consumer avatar name: %w", err)
	}
	name := hex.EncodeToString(raw) + extension
	path := filepath.Join(store.root, name)
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return "", fmt.Errorf("write consumer avatar: %w", err)
	}
	return name, nil
}

func (store *LocalConsumerAvatarStore) Open(name string) (*os.File, error) {
	if !consumerAvatarFilePattern.MatchString(name) {
		return nil, os.ErrNotExist
	}
	return os.Open(filepath.Join(store.root, name))
}

func (store *LocalConsumerAvatarStore) Remove(name string) error {
	if !consumerAvatarFilePattern.MatchString(name) {
		return os.ErrNotExist
	}
	return os.Remove(filepath.Join(store.root, name))
}

// ConsumerAvatarPath returns the public relative path of one stored avatar.
func ConsumerAvatarPath(name string) string {
	return consumerAvatarPublicPrefix + name
}

// consumerIdentityWriter is the PostgreSQL write boundary for the two
// Auth-owned identity fields the consumer can edit in this runtime.
type consumerIdentityWriter interface {
	UpdateNickname(
		context.Context,
		uuid.UUID,
		string,
		string,
	) (nickname string, avatarURL string, principalProfileETag string, err error)
	UpdateAvatarURL(
		context.Context,
		uuid.UUID,
		string,
	) (previous string, err error)
}

type ConsumerIdentityHandler struct {
	writer              consumerIdentityWriter
	avatars             ConsumerAvatarStore
	legacyAvatars       ConsumerLegacyAvatarReader
	principal           PrincipalResolver
	moderator           AvatarModerator
	nicknameModerator   ConsumerNicknameModerator
	registrationContact registrationContactApplication
}

// SetLegacyAvatarReader wires the narrow compatibility read used for old Auth
// avatar references. A nil reader leaves the route fail closed.
func (handler *ConsumerIdentityHandler) SetLegacyAvatarReader(
	reader ConsumerLegacyAvatarReader,
) {
	if handler == nil {
		return
	}
	handler.legacyAvatars = reader
}

func NewConsumerIdentityHandler(
	writer consumerIdentityWriter,
	avatars ConsumerAvatarStore,
	principal PrincipalResolver,
	moderator AvatarModerator,
	nicknameModerator ConsumerNicknameModerator,
) *ConsumerIdentityHandler {
	return &ConsumerIdentityHandler{
		writer:            writer,
		avatars:           avatars,
		principal:         principal,
		moderator:         moderator,
		nicknameModerator: nicknameModerator,
	}
}

func (handler *ConsumerIdentityHandler) RegisterAuthenticatedRoutes(
	group *gin.RouterGroup,
) {
	group.PATCH("/me/profile/nickname", handler.UpdateNickname)
	group.POST("/me/avatar", handler.UploadAvatar)
	group.GET("/me/registration-contact", handler.GetRegistrationContact)
	group.PATCH("/me/registration-contact", handler.UpdateRegistrationContact)
}

func (handler *ConsumerIdentityHandler) RegisterPublicRoutes(
	group *gin.RouterGroup,
) {
	group.GET("/avatars/:filename", handler.GetAvatar)
	group.HEAD("/avatars/:filename", handler.GetAvatar)
	group.GET(
		"/avatars/legacy/:principal_id/:file_id",
		handler.GetLegacyAvatar,
	)
	group.HEAD(
		"/avatars/legacy/:principal_id/:file_id",
		handler.GetLegacyAvatar,
	)
}

// UpdateNickname godoc
// @Summary Update the authenticated consumer's nickname
// @Description BUSINESS-STATE command. Tenant and Principal are server-derived from the consumer session; the trimmed nickname (1..64 runes) is content-moderated, then written only when principal_profile_etag still matches the current profile. The refreshed identity projection and new ETag are returned.
// @Tags xiangwan
// @Accept json
// @Produce json
// @Param request body ConsumerNicknameHTTPRequest true "New nickname"
// @Security BearerAuth
// @Success 200 {object} ConsumerNicknameResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 503 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/profile/nickname [patch]
func (handler *ConsumerIdentityHandler) UpdateNickname(c *gin.Context) {
	if handler == nil || handler.writer == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan consumer identity is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || c.ContentType() != "application/json" {
		writeError(c, errx.NewBadRequest("invalid nickname request"))
		return
	}
	payload, err := decodeConsumerNicknameRequest(c)
	if err != nil {
		writeError(c, err)
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	nickname, ok := normalizeConsumerNickname(*payload.Nickname)
	if !ok {
		writeError(c, errx.NewBadRequest("invalid nickname"))
		return
	}
	if handler.nicknameModerator == nil {
		writeConsumerIdentityError(c, ErrNicknameModerationUnavailable)
		return
	}
	if err := handler.nicknameModerator.CheckNickname(
		c.Request.Context(), principalID, nickname,
	); err != nil {
		writeConsumerIdentityError(c, err)
		return
	}
	updatedNickname, avatarURL, principalProfileETag, err := handler.writer.UpdateNickname(
		c.Request.Context(),
		principalID,
		nickname,
		*payload.ExpectedPrincipalProfileETag,
	)
	if err != nil {
		writeConsumerIdentityError(c, err)
		return
	}
	response.OK(c, ConsumerNicknameResponse{
		Nickname:             updatedNickname,
		AvatarURL:            avatarURL,
		PrincipalProfileETag: principalProfileETag,
	})
}

// UploadAvatar godoc
// @Summary Upload the authenticated consumer's avatar
// @Description Stores one JPEG/PNG/WebP avatar (max 1 MiB) for the session Principal after a server-side WeChat img_sec_check content-security review, repoints avatar_url to the immutable public object, and returns its relative URL. A rejected or unreviewable image is refused and nothing is stored (fail closed); the replaced avatar object is kept — published avatar objects are immutable and only a delayed GC may collect stale ones.
// @Tags xiangwan
// @Accept multipart/form-data
// @Produce json
// @Param image formData file true "Avatar image"
// @Security BearerAuth
// @Success 200 {object} ConsumerAvatarResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/avatar [post]
func (handler *ConsumerIdentityHandler) UploadAvatar(c *gin.Context) {
	if handler == nil || handler.writer == nil || handler.avatars == nil ||
		handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan consumer identity is unavailable"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	if c.ContentType() != "multipart/form-data" {
		writeError(c, errx.NewBadRequest("invalid avatar request"))
		return
	}
	// The byte reader caps the whole multipart body, framing overhead
	// included; the per-file size check below stays the real file bound.
	c.Request.Body = http.MaxBytesReader(
		c.Writer, c.Request.Body, maxConsumerAvatarUploadBytes,
	)
	fileHeader, fileErr := c.FormFile("image")
	if fileErr != nil {
		writeError(c, errx.NewBadRequest("invalid avatar request"))
		return
	}
	if fileHeader.Size > maxConsumerAvatarBytes {
		writeError(c, errx.NewBadRequest("avatar image is too large"))
		return
	}
	file, openErr := fileHeader.Open()
	if openErr != nil {
		writeError(c, errx.NewBadRequest("invalid avatar request"))
		return
	}
	defer func() { _ = file.Close() }()
	data := make([]byte, fileHeader.Size)
	if _, readErr := io.ReadFull(file, data); readErr != nil {
		writeError(c, errx.NewBadRequest("invalid avatar request"))
		return
	}
	extension, accepted := consumerAvatarExtensions[detectImageContentType(data)]
	if !accepted {
		writeError(c, errx.NewBadRequest("avatar image must be JPEG, PNG, or WebP"))
		return
	}
	// Server-side content-security gate: a direct HTTP client can bypass the
	// WeChat chooseAvatar component, so the review must happen here, before
	// anything is stored. A nil moderator means the gate was explicitly
	// disabled (local dev switch); every configured outcome is fail-closed.
	if handler.moderator != nil {
		moderationErr := handler.moderator.CheckAvatarImage(
			c.Request.Context(), data, "avatar"+extension,
		)
		switch {
		case errors.Is(moderationErr, ErrAvatarImageRejected):
			writeError(c, errx.NewBadRequest(avatarRejectedCustomerMessage))
			return
		case moderationErr != nil:
			// The WeChat client layer sanitizes transport errors (the
			// img_sec_check URL carries access_token, the token endpoint
			// carries appid/secret), so the attached error records only the
			// classified failure and can never write credentials into the
			// access log.
			_ = c.Error(moderationErr)
			writeError(c, errx.NewBadRequest(avatarModerationUnavailableCustomerMessage))
			return
		}
	}
	name, saveErr := handler.avatars.Save(data, extension)
	if saveErr != nil {
		_ = c.Error(saveErr)
		writeError(c, errx.NewInternal("avatar upload failed"))
		return
	}
	avatarURL := ConsumerAvatarPath(name)
	if _, err := handler.writer.UpdateAvatarURL(
		c.Request.Context(),
		principalID,
		avatarURL,
	); err != nil {
		// The new object is orphaned once the Principal write fails; remove it
		// best-effort so the store never accumulates unreferenced uploads.
		if removeErr := handler.avatars.Remove(name); removeErr != nil {
			_ = c.Error(fmt.Errorf("remove orphaned consumer avatar: %w", removeErr))
		}
		writeConsumerIdentityError(c, err)
		return
	}
	// The replaced object is deliberately NOT removed: published avatar
	// objects answer immutable-cache reads forever, so an old file may still
	// be served from cache even after avatar_url moved on. Stale objects are
	// collected by a delayed GC instead (docs/architecture/upgrade-backlog.md).
	response.OK(c, ConsumerAvatarResponse{AvatarURL: avatarURL})
}

// GetAvatar godoc
// @Summary Read one Xiangwan consumer avatar image
// @Description Anonymous immutable read of one uploaded avatar; filenames are server-generated and never reused.
// @Tags xiangwan
// @Produce application/octet-stream
// @Param filename path string true "Server-generated avatar filename"
// @Success 200 {file} binary
// @Failure 404 {object} response.Body
// @Router /xiangwan/avatars/{filename} [get]
func (handler *ConsumerIdentityHandler) GetAvatar(c *gin.Context) {
	if handler == nil || handler.avatars == nil {
		writeError(c, errx.NewInternal("avatar media is unavailable"))
		return
	}
	object, err := handler.avatars.Open(c.Param("filename"))
	if err != nil {
		writeError(c, errx.NewNotFound("avatar not found"))
		return
	}
	defer func() { _ = object.Close() }()
	c.Header("Content-Type", consumerAvatarContentType(c.Param("filename")))
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(c.Writer, c.Request, c.Param("filename"), time.Time{}, object)
}

// GetLegacyAvatar serves a currently published platform avatar through the
// Xiangwan runtime. The compatibility reader owns the live Principal/file
// binding and confirmed-object checks; this handler adds the image contract
// and deliberately uses no-store because the URL remains revocable.
func (handler *ConsumerIdentityHandler) GetLegacyAvatar(c *gin.Context) {
	if handler == nil || handler.legacyAvatars == nil {
		writeError(c, errx.NewNotFound("avatar not found"))
		return
	}
	principalID, principalErr := parseCanonicalUUID(c.Param("principal_id"))
	fileID, fileErr := parseCanonicalUUID(c.Param("file_id"))
	if principalErr != nil || fileErr != nil {
		writeError(c, errx.NewBadRequest("invalid avatar reference"))
		return
	}
	object, err := handler.legacyAvatars.Open(
		c.Request.Context(), principalID, fileID,
	)
	if err != nil || !validLegacyAvatarObject(object, fileID) {
		if object != nil {
			_ = object.Close()
		}
		writeError(c, errx.NewNotFound("avatar not found"))
		return
	}
	defer func() { _ = object.Close() }()
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Type", strings.ToLower(strings.TrimSpace(object.MIME)))
	http.ServeContent(
		c.Writer,
		c.Request,
		fileID.String(),
		time.Time{},
		object,
	)
}

type ConsumerNicknameHTTPRequest struct {
	Nickname                     *string `json:"nickname" binding:"required"`
	ExpectedPrincipalProfileETag *string `json:"principal_profile_etag" binding:"required"`
}

type ConsumerNicknameResponse struct {
	Nickname             string `json:"nickname"`
	AvatarURL            string `json:"avatar_url,omitempty"`
	PrincipalProfileETag string `json:"principal_profile_etag"`
}

type ConsumerAvatarResponse struct {
	AvatarURL string `json:"avatar_url"`
}

func decodeConsumerNicknameRequest(
	c *gin.Context,
) (ConsumerNicknameHTTPRequest, error) {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxConsumerNicknameBytes,
	)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var payload ConsumerNicknameHTTPRequest
	if err := decoder.Decode(&payload); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return ConsumerNicknameHTTPRequest{}, errx.New(
				errx.CodeFileTooLarge,
				"nickname request is too large",
			)
		}
		return ConsumerNicknameHTTPRequest{},
			errx.NewBadRequest("invalid nickname request body")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || payload.Nickname == nil ||
		payload.ExpectedPrincipalProfileETag == nil ||
		strings.TrimSpace(*payload.ExpectedPrincipalProfileETag) == "" {
		return ConsumerNicknameHTTPRequest{},
			errx.NewBadRequest("invalid nickname request body")
	}
	return payload, nil
}

// normalizeConsumerNickname trims surrounding whitespace and enforces the
// Principals column shape: 1..64 runes, no control characters.
func normalizeConsumerNickname(value string) (string, bool) {
	nickname := strings.TrimSpace(value)
	runes := utf8.RuneCountInString(nickname)
	if runes < 1 || runes > maxConsumerNicknameRunes {
		return "", false
	}
	if strings.IndexFunc(nickname, unicode.IsControl) >= 0 {
		return "", false
	}
	return nickname, true
}

func writeConsumerIdentityError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNicknameRejected):
		writeError(c, errx.NewBadRequest("nickname was rejected by content security"))
	case errors.Is(err, ErrNicknameModerationUnavailable):
		c.PureJSON(http.StatusServiceUnavailable, response.Body{
			Code:    int(errx.CodeInternal),
			Message: "service unavailable",
		})
	case errors.Is(err, identitypostgres.ErrProfileETagConflict):
		writeError(c, errx.NewConflict("profile changed; refresh and retry"))
	case errors.Is(err, identitypostgres.ErrPrincipalUnavailable):
		writeError(c, errx.NewForbidden("account is unavailable"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan consumer identity failed"))
	}
}

func consumerAvatarContentType(name string) string {
	for mediaType, extension := range consumerAvatarExtensions {
		if extension == strings.ToLower(filepath.Ext(name)) {
			return mediaType
		}
	}
	return "application/octet-stream"
}

// ConsumerLegacyAvatarPath returns the Xiangwan compatibility address for a
// platform Auth avatar file. It is only an address: GetLegacyAvatar repeats
// the live Principal, ownership, confirmation, and object checks.
func ConsumerLegacyAvatarPath(principalID, fileID uuid.UUID) string {
	if principalID == uuid.Nil || fileID == uuid.Nil {
		return ""
	}
	return consumerLegacyAvatarPrefix + principalID.String() + "/" + fileID.String()
}

func validLegacyAvatarObject(object *publicobject.Object, fileID uuid.UUID) bool {
	if object == nil || object.Content == nil || object.Closer == nil ||
		fileID == uuid.Nil || object.FileID != fileID || object.Size <= 0 ||
		object.Size > maxLegacyAvatarBytes {
		return false
	}
	mimeType := strings.ToLower(strings.TrimSpace(object.MIME))
	detected := strings.ToLower(strings.TrimSpace(object.DetectedMIME))
	return consumerLegacyAvatarMIMEs[mimeType] && detected == mimeType
}
