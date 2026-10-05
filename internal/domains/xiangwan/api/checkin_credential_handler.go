package xiangwanapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	checkinpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type checkinCredentialApplication interface {
	Issue(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (checkin.IssuedCredential, error)
}

type CheckinCredentialHandler struct {
	service   checkinCredentialApplication
	principal PrincipalResolver
}

func NewCheckinCredentialHandler(
	service checkinCredentialApplication,
	principal PrincipalResolver,
) *CheckinCredentialHandler {
	return &CheckinCredentialHandler{service: service, principal: principal}
}

func (handler *CheckinCredentialHandler) RegisterRoutes(
	group *gin.RouterGroup,
) {
	group.POST(
		"/registrations/:registration_id/checkin-credentials",
		handler.IssueCheckinCredential,
	)
}

// IssueCheckinCredential godoc
// @Summary Issue a short-lived Checkin credential for one owned Registration
// @Description EPHEMERAL-ISSUE command. It accepts no body or idempotency key, rotates any previous credential in PostgreSQL, and returns QR and backup plaintext only once. An uncertain response must be replaced by a fresh issue request.
// @Tags xiangwan
// @Produce json
// @Param registration_id path string true "Registration ID"
// @Security BearerAuth
// @Success 200 {object} CheckinCredentialResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/registrations/{registration_id}/checkin-credentials [post]
func (handler *CheckinCredentialHandler) IssueCheckinCredential(
	c *gin.Context,
) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan Checkin credential is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || !emptyJSONCompatibleRequest(c) ||
		len(c.Request.Header.Values("Idempotency-Key")) != 0 {
		writeError(c, errx.NewBadRequest("invalid Checkin credential request"))
		return
	}
	registrationID, err := parseCanonicalUUID(c.Param("registration_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Registration id"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	issued, err := handler.service.Issue(
		c.Request.Context(),
		principalID,
		registrationID,
	)
	if err != nil {
		writeCheckinCredentialError(c, err)
		return
	}
	response.OK(c, projectCheckinCredentialResponse(issued))
}

func writeCheckinCredentialError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidCheckinCredentialRequest),
		errors.Is(err, checkinpostgres.ErrInvalidIssueRegistrationCredentialCommand):
		writeError(c, errx.NewBadRequest("invalid Checkin credential request"))
	case errors.Is(err, checkinpostgres.ErrRegistrationCredentialUnavailable):
		writeError(c, errx.NewConflict("Checkin credential is unavailable"))
	case errors.Is(err, checkinpostgres.ErrRegistrationCredentialRateLimited):
		c.Header(
			"Retry-After",
			strconv.Itoa(checkinpostgres.CredentialReissueRetryAfterSeconds),
		)
		c.PureJSON(http.StatusTooManyRequests, response.Body{
			Code:    42900,
			Message: "Checkin credential was issued recently; retry later",
		})
	case errors.Is(err, checkinpostgres.ErrRegistrationCredentialGenerationInactive):
		c.PureJSON(http.StatusServiceUnavailable, response.Body{
			Code:    int(errx.CodeInternal),
			Message: "service unavailable",
		})
	case errors.Is(
		err,
		checkinpostgres.ErrRegistrationCredentialTransactionConflict,
	):
		writeError(c, errx.NewConflict("Registration changed; refresh and retry"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan Checkin credential failed"))
	}
}

func emptyJSONCompatibleRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.ContentLength > 0 ||
		len(c.Request.TransferEncoding) != 0 {
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

type CheckinCredentialResponse struct {
	RegistrationID  string `json:"registration_id"`
	SeriesID        string `json:"series_id"`
	InstanceID      string `json:"instance_id"`
	SessionID       string `json:"session_id"`
	CredentialJTI   string `json:"credential_jti"`
	CredentialEpoch int64  `json:"credential_epoch"`
	QRToken         string `json:"qr_token"`
	BackupCode      string `json:"backup_code"`
	IssuedAt        string `json:"issued_at"`
	ExpiresAt       string `json:"expires_at"`
}

func projectCheckinCredentialResponse(
	issued checkin.IssuedCredential,
) CheckinCredentialResponse {
	credential := issued.Credential
	return CheckinCredentialResponse{
		RegistrationID:  credential.RegistrationID.String(),
		SeriesID:        credential.SeriesID.String(),
		InstanceID:      credential.InstanceID.String(),
		SessionID:       credential.SessionID.String(),
		CredentialJTI:   credential.CredentialJTI.String(),
		CredentialEpoch: credential.CredentialEpoch,
		QRToken:         issued.QRToken,
		BackupCode:      issued.BackupCode,
		IssuedAt:        formatMyRegistrationTime(credential.IssuedAt),
		ExpiresAt:       formatMyRegistrationTime(credential.ExpiresAt),
	}
}
