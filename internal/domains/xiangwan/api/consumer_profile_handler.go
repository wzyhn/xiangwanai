package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile"
	consumerprofilepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxConsumerProfileBodyBytes = 16 * 1024

type consumerProfileApplication interface {
	GetMine(context.Context, uuid.UUID) (consumerprofile.Snapshot, error)
	Update(
		context.Context,
		uuid.UUID,
		ConsumerProfileUpdateRequest,
	) (consumerprofile.MutationReceipt, error)
}

type ConsumerProfileHandler struct {
	service   consumerProfileApplication
	principal PrincipalResolver
}

func NewConsumerProfileHandler(
	service consumerProfileApplication,
	principal PrincipalResolver,
) *ConsumerProfileHandler {
	return &ConsumerProfileHandler{service: service, principal: principal}
}

func (handler *ConsumerProfileHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/me/profile", handler.GetMine)
	group.PATCH("/me/profile", handler.Update)
}

// GetMine godoc
// @Summary Read the authenticated consumer's Xiangwan profile
// @Description Combines Auth-owned nickname and approved avatar with the Xiangwan-owned published occupation, introduction, tags, visibility choices, and latest pending update. The two authorities remain separate. Provider subjects, operation keys, moderation evidence, and internal timestamps never cross the response.
// @Tags xiangwan
// @Produce json
// @Security BearerAuth
// @Success 200 {object} ConsumerProfileResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/profile [get]
func (handler *ConsumerProfileHandler) GetMine(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan ConsumerProfile is unavailable"))
		return
	}
	if !emptyConsumerProfileReadRequest(c) {
		writeError(c, errx.NewBadRequest("invalid ConsumerProfile request"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	snapshot, err := handler.service.GetMine(c.Request.Context(), principalID)
	if err != nil {
		writeConsumerProfileError(c, err)
		return
	}
	response.OK(c, projectConsumerProfile(principalID, snapshot))
}

// emptyConsumerProfileReadRequest keeps the GET contract bodyless while
// tolerating the harmless application/json header that some WeChat runtimes
// add even when wx.request has no data. Query strings, transfer encodings,
// non-JSON media types, and any body remain invalid.
func emptyConsumerProfileReadRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil || len(c.Request.URL.Query()) != 0 ||
		c.Request.ContentLength != 0 || len(c.Request.TransferEncoding) != 0 ||
		len(c.Request.Header.Values("Idempotency-Key")) != 0 {
		return false
	}
	if contentType := c.GetHeader("Content-Type"); contentType != "" &&
		c.ContentType() != "application/json" {
		return false
	}
	if c.Request.Body == nil {
		return true
	}
	payload, err := io.ReadAll(io.LimitReader(c.Request.Body, 1))
	return err == nil && len(payload) == 0
}

// Update godoc
// @Summary Replace the authenticated consumer's Xiangwan profile extension
// @Description OP-KEY command. Replaces only occupation, introduction, tags, and explicit visibility choices using the expected ConsumerProfile version and current server-owned privacy policy. Only a pass with durable provider evidence publishes; review or provider unavailability persists a retryable candidate and preserves the previous publication. Nickname, avatar, phone, identity, and role fields are rejected as unknown.
// @Tags xiangwan
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body ConsumerProfileUpdateHTTPRequest true "Complete Xiangwan profile extension candidate"
// @Security BearerAuth
// @Success 200 {object} ConsumerProfileUpdateResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 413 {object} response.Body
// @Failure 500 {object} response.Body
// @Failure 503 {object} response.Body
// @Router /xiangwan/me/profile [patch]
func (handler *ConsumerProfileHandler) Update(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan ConsumerProfile is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || c.ContentType() != "application/json" {
		writeError(c, errx.NewBadRequest("invalid ConsumerProfile request"))
		return
	}
	operationKey, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	request, err := decodeConsumerProfileUpdate(c)
	if err != nil {
		writeError(c, err)
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	receipt, err := handler.service.Update(
		c.Request.Context(),
		principalID,
		ConsumerProfileUpdateRequest{
			OperationKey:    operationKey,
			ExpectedVersion: *request.ExpectedVersion,
			Fields: consumerprofile.Fields{
				Occupation:   *request.Occupation,
				Introduction: *request.Introduction,
				Tags:         *request.Tags,
				Visibility: consumerprofile.Visibility{
					Occupation:   *request.Visibility.Occupation,
					Introduction: *request.Visibility.Introduction,
					Tags:         *request.Visibility.Tags,
				},
			},
		},
	)
	if err != nil {
		writeConsumerProfileError(c, err)
		return
	}
	response.OK(c, projectConsumerProfileUpdate(receipt))
}

type ConsumerProfileVisibilityHTTPRequest struct {
	Occupation   *bool `json:"occupation" binding:"required"`
	Introduction *bool `json:"introduction" binding:"required"`
	Tags         *bool `json:"tags" binding:"required"`
}

type ConsumerProfileUpdateHTTPRequest struct {
	ExpectedVersion *int64                                `json:"expected_version" binding:"required"`
	Occupation      *string                               `json:"occupation" binding:"required"`
	Introduction    *string                               `json:"introduction" binding:"required"`
	Tags            *[]string                             `json:"tags" binding:"required"`
	Visibility      *ConsumerProfileVisibilityHTTPRequest `json:"visibility" binding:"required"`
}

type ConsumerProfileVisibilityResponse struct {
	Occupation   bool `json:"occupation"`
	Introduction bool `json:"introduction"`
	Tags         bool `json:"tags"`
}

type ConsumerProfileFieldsResponse struct {
	Occupation   string                            `json:"occupation"`
	Introduction string                            `json:"introduction"`
	Tags         []string                          `json:"tags"`
	Visibility   ConsumerProfileVisibilityResponse `json:"visibility"`
}

type ConsumerProfilePublishedResponse struct {
	ConsumerProfileFieldsResponse
	PrivacyPolicyVersion string `json:"privacy_policy_version,omitempty"`
	Version              int64  `json:"version"`
	UpdatedAt            string `json:"updated_at,omitempty"`
}

type ConsumerProfilePendingResponse struct {
	ConsumerProfileFieldsResponse
	CandidateVersion int64  `json:"candidate_version"`
	SubmittedAt      string `json:"submitted_at"`
}

type ConsumerProfileResponse struct {
	Nickname             string                           `json:"nickname"`
	AvatarURL            string                           `json:"avatar_url,omitempty"`
	PrincipalProfileETag string                           `json:"principal_profile_etag"`
	Profile              ConsumerProfilePublishedResponse `json:"profile"`
	PendingUpdate        *ConsumerProfilePendingResponse  `json:"pending_update,omitempty"`
}

type ConsumerProfileUpdateResponse struct {
	Candidate            ConsumerProfileFieldsResponse    `json:"candidate"`
	CandidateVersion     int64                            `json:"candidate_version"`
	ModerationStatus     consumerprofile.ModerationStatus `json:"moderation_status"`
	PublishedVersion     int64                            `json:"published_version,omitempty"`
	PrivacyPolicyVersion string                           `json:"privacy_policy_version"`
	SubmittedAt          string                           `json:"submitted_at"`
	Replayed             bool                             `json:"replayed"`
}

func decodeConsumerProfileUpdate(
	c *gin.Context,
) (ConsumerProfileUpdateHTTPRequest, error) {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxConsumerProfileBodyBytes,
	)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var payload ConsumerProfileUpdateHTTPRequest
	if err := decoder.Decode(&payload); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return ConsumerProfileUpdateHTTPRequest{}, errx.New(
				errx.CodeFileTooLarge,
				"ConsumerProfile request is too large",
			)
		}
		return ConsumerProfileUpdateHTTPRequest{},
			errx.NewBadRequest("invalid ConsumerProfile request body")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF ||
		payload.ExpectedVersion == nil || payload.Occupation == nil ||
		payload.Introduction == nil || payload.Tags == nil ||
		payload.Visibility == nil || payload.Visibility.Occupation == nil ||
		payload.Visibility.Introduction == nil || payload.Visibility.Tags == nil {
		return ConsumerProfileUpdateHTTPRequest{},
			errx.NewBadRequest("invalid ConsumerProfile request body")
	}
	return payload, nil
}

func writeConsumerProfileError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidConsumerProfileRequest),
		errors.Is(err, consumerprofile.ErrInvalidFields),
		errors.Is(err, consumerprofile.ErrInvalidPatch),
		errors.Is(err, consumerprofilepostgres.ErrInvalidRepository):
		writeError(c, errx.NewBadRequest("invalid ConsumerProfile request"))
	case errors.Is(err, ErrConsumerProfileContentRejected):
		writeError(c, errx.NewBadRequest("profile content was rejected"))
	case errors.Is(err, consumerprofilepostgres.ErrPrincipalUnavailable),
		errors.Is(err, consumerprofilepostgres.ErrProviderIdentityUnavailable):
		writeError(c, errx.NewForbidden("ConsumerProfile is unavailable"))
	case errors.Is(err, consumerprofilepostgres.ErrOperationConflict):
		writeError(c, errx.NewConflict("Idempotency-Key conflicts with another request"))
	case errors.Is(err, consumerprofilepostgres.ErrVersionConflict):
		writeError(c, errx.NewConflict("ConsumerProfile changed; refresh and retry"))
	case errors.Is(err, consumerprofilepostgres.ErrTransactionConflict),
		errors.Is(err, consumerprofilepostgres.ErrGenerationInactive):
		c.PureJSON(http.StatusServiceUnavailable, response.Body{
			Code:    int(errx.CodeInternal),
			Message: "service unavailable",
		})
	case errors.Is(err, ErrConsumerProfilePolicyUnavailable),
		errors.Is(err, ErrConsumerProfileModerationUnavailable):
		c.PureJSON(http.StatusServiceUnavailable, response.Body{
			Code:    int(errx.CodeInternal),
			Message: "service unavailable",
		})
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan ConsumerProfile failed"))
	}
}

func projectConsumerProfile(
	principalID uuid.UUID,
	value consumerprofile.Snapshot,
) ConsumerProfileResponse {
	result := ConsumerProfileResponse{
		Nickname:             value.Principal.Nickname,
		AvatarURL:            projectConsumerAvatarURL(principalID, value.Principal),
		PrincipalProfileETag: value.Principal.ETag,
		Profile: ConsumerProfilePublishedResponse{
			ConsumerProfileFieldsResponse: projectConsumerProfileFields(
				value.Published.Fields,
			),
			PrivacyPolicyVersion: value.Published.PrivacyPolicyVersion,
			Version:              value.Published.Version,
		},
	}
	if !value.Published.UpdatedAt.IsZero() {
		result.Profile.UpdatedAt = formatMyRegistrationTime(
			value.Published.UpdatedAt,
		)
	}
	if value.Pending != nil {
		result.PendingUpdate = &ConsumerProfilePendingResponse{
			ConsumerProfileFieldsResponse: projectConsumerProfileFields(
				value.Pending.Fields,
			),
			CandidateVersion: value.Pending.CandidateVersion,
			SubmittedAt: formatMyRegistrationTime(
				value.Pending.SubmittedAt,
			),
		}
	}
	return result
}

// projectConsumerAvatarURL moves only known legacy platform paths to
// Xiangwan's strict compatibility reader. Arbitrary URLs and mismatched
// principal/file identities remain untouched and are subsequently rejected by
// the client media allowlist instead of being turned into a guessed address.
func projectConsumerAvatarURL(
	principalID uuid.UUID,
	value consumerprofile.PrincipalProfile,
) string {
	raw := strings.TrimSpace(value.AvatarURL)
	if principalID == uuid.Nil || value.AvatarFileID == nil ||
		*value.AvatarFileID == uuid.Nil {
		return raw
	}
	expected := "/api/v1/auth/principals/" + principalID.String() +
		"/avatar/" + value.AvatarFileID.String()
	legacyFilePath := "/api/v1/files/" + value.AvatarFileID.String() + "/content"
	if raw != expected && raw != legacyFilePath &&
		!strings.HasPrefix(raw, legacyFilePath+"?") {
		return raw
	}
	return ConsumerLegacyAvatarPath(principalID, *value.AvatarFileID)
}

func projectConsumerProfileUpdate(
	value consumerprofile.MutationReceipt,
) ConsumerProfileUpdateResponse {
	return ConsumerProfileUpdateResponse{
		Candidate:            projectConsumerProfileFields(value.Fields),
		CandidateVersion:     value.CandidateVersion,
		ModerationStatus:     value.ModerationStatus,
		PublishedVersion:     value.PublishedVersion,
		PrivacyPolicyVersion: value.PrivacyVersion,
		SubmittedAt:          formatMyRegistrationTime(value.SubmittedAt),
		Replayed:             value.Replayed,
	}
}

func projectConsumerProfileFields(
	value consumerprofile.Fields,
) ConsumerProfileFieldsResponse {
	return ConsumerProfileFieldsResponse{
		Occupation:   value.Occupation,
		Introduction: value.Introduction,
		Tags:         append([]string(nil), value.Tags...),
		Visibility: ConsumerProfileVisibilityResponse{
			Occupation:   value.Visibility.Occupation,
			Introduction: value.Visibility.Introduction,
			Tags:         value.Visibility.Tags,
		},
	}
}
