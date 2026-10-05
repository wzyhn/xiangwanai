package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	checkinpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxOnsiteCheckinBodyBytes = 4 * 1024

type onsiteCheckinApplication interface {
	Verify(
		context.Context,
		xiangwanadmin.Principal,
		OnsiteCheckinVerificationRequest,
	) (checkinpostgres.VerifyCredentialResult, error)
	Record(
		context.Context,
		xiangwanadmin.Principal,
		OnsiteCheckinRecordRequest,
	) (checkinpostgres.RecordCheckinResult, error)
}

// OnsiteAdminPrincipalResolver is intentionally distinct from the consumer
// PrincipalResolver so the customer JWT authenticator cannot be wired here by
// accident. Implementations must resolve a live Xiangwan administrator
// session and its mapped Principal.
type OnsiteAdminPrincipalResolver interface {
	ResolveOnsiteAdminPrincipal(*gin.Context) (xiangwanadmin.Principal, error)
}

// OnsiteCheckinHandler must be mounted only behind the independent Xiangwan
// administrator authentication boundary. A consumer PrincipalResolver is not
// an acceptable substitute; PostgreSQL authorization is rechecked by the
// service delegates inside both command transactions.
type OnsiteCheckinHandler struct {
	service   onsiteCheckinApplication
	principal OnsiteAdminPrincipalResolver
}

func NewOnsiteCheckinHandler(
	service onsiteCheckinApplication,
	principal OnsiteAdminPrincipalResolver,
) *OnsiteCheckinHandler {
	return &OnsiteCheckinHandler{service: service, principal: principal}
}

func (handler *OnsiteCheckinHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.POST("/checkin-verifications", handler.VerifyCredential)
	group.POST(
		"/registrations/:registration_id/checkins",
		handler.RecordCheckin,
	)
}

// VerifyCredential godoc
// @Summary Verify one Xiangwan on-site Checkin credential
// @Description ADMIN OP-KEY command. Verifies a QR token or backup code against one exact Session, records a minimal immutable decision in PostgreSQL, and returns no participant identity or presented secret.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body OnsiteCheckinVerificationHTTPRequest true "Exact Session and presented credential"
// @Success 200 {object} OnsiteCheckinVerificationResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 413 {object} response.Body
// @Failure 500 {object} response.Body
// @Failure 503 {object} response.Body
// @Router /xiangwan/admin/checkin-verifications [post]
func (handler *OnsiteCheckinHandler) VerifyCredential(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan Checkin verification is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || c.ContentType() != "application/json" {
		writeError(c, errx.NewBadRequest("invalid Checkin verification request"))
		return
	}
	operationKey, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	var payload OnsiteCheckinVerificationHTTPRequest
	if err := decodeOnsiteCheckinJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	request, err := payload.request(operationKey)
	if err != nil {
		writeError(c, err)
		return
	}
	operator, err := handler.principal.ResolveOnsiteAdminPrincipal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	result, err := handler.service.Verify(
		c.Request.Context(),
		operator,
		request,
	)
	if err != nil {
		writeOnsiteCheckinError(c, err)
		return
	}
	response.OK(c, projectOnsiteCheckinVerificationResponse(result))
}

// RecordCheckin godoc
// @Summary Record one verified Xiangwan on-site Checkin
// @Description ADMIN BUSINESS-STATE command. Accepts no client Idempotency-Key. Rechecks operator authorization, the exact confirmed Registration, verification receipt, active credential, and Session before atomically creating the unique Checkin fact/event and incrementing attendance once; duplicate scans return the current fact, including a later revoked state.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param registration_id path string true "Registration ID returned by a valid verification"
// @Param request body OnsiteCheckinRecordHTTPRequest true "Verified exact-Session facts"
// @Success 200 {object} OnsiteCheckinRecordResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 413 {object} response.Body
// @Failure 500 {object} response.Body
// @Failure 503 {object} response.Body
// @Router /xiangwan/admin/registrations/{registration_id}/checkins [post]
func (handler *OnsiteCheckinHandler) RecordCheckin(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan Checkin recording is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || c.ContentType() != "application/json" ||
		len(c.Request.Header.Values("Idempotency-Key")) != 0 {
		writeError(c, errx.NewBadRequest("invalid Checkin recording request"))
		return
	}
	registrationID, err := parseCanonicalUUID(c.Param("registration_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Registration id"))
		return
	}
	var payload OnsiteCheckinRecordHTTPRequest
	if err := decodeOnsiteCheckinJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	request, err := payload.request(registrationID)
	if err != nil {
		writeError(c, err)
		return
	}
	operator, err := handler.principal.ResolveOnsiteAdminPrincipal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	result, err := handler.service.Record(
		c.Request.Context(),
		operator,
		request,
	)
	if err != nil {
		writeOnsiteCheckinError(c, err)
		return
	}
	response.OK(c, projectOnsiteCheckinRecordResponse(result))
}

type OnsiteCheckinVerificationHTTPRequest struct {
	SeriesID       string                          `json:"series_id"`
	InstanceID     string                          `json:"instance_id"`
	SessionID      string                          `json:"session_id"`
	PresentedKind  checkin.PresentedCredentialKind `json:"presented_kind"`
	PresentedValue string                          `json:"presented_value"`
}

func (payload OnsiteCheckinVerificationHTTPRequest) request(
	operationKey uuid.UUID,
) (OnsiteCheckinVerificationRequest, error) {
	seriesID, seriesErr := parseCanonicalUUID(payload.SeriesID)
	instanceID, instanceErr := parseCanonicalUUID(payload.InstanceID)
	sessionID, sessionErr := parseCanonicalUUID(payload.SessionID)
	if seriesErr != nil || instanceErr != nil || sessionErr != nil ||
		(payload.PresentedKind != checkin.PresentedCredentialKindQRToken &&
			payload.PresentedKind != checkin.PresentedCredentialKindBackupCode) ||
		payload.PresentedValue == "" ||
		len(payload.PresentedValue) > checkin.MaxPresentedCredentialLength {
		return OnsiteCheckinVerificationRequest{},
			errx.NewBadRequest("invalid Checkin verification request body")
	}
	return OnsiteCheckinVerificationRequest{
		SeriesID:       seriesID,
		InstanceID:     instanceID,
		SessionID:      sessionID,
		PresentedKind:  payload.PresentedKind,
		PresentedValue: payload.PresentedValue,
		IdempotencyKey: operationKey,
	}, nil
}

type OnsiteCheckinRecordHTTPRequest struct {
	SeriesID              string `json:"series_id"`
	InstanceID            string `json:"instance_id"`
	SessionID             string `json:"session_id"`
	CredentialID          string `json:"credential_id"`
	VerificationAttemptID string `json:"verification_attempt_id"`
}

func (payload OnsiteCheckinRecordHTTPRequest) request(
	registrationID uuid.UUID,
) (OnsiteCheckinRecordRequest, error) {
	seriesID, seriesErr := parseCanonicalUUID(payload.SeriesID)
	instanceID, instanceErr := parseCanonicalUUID(payload.InstanceID)
	sessionID, sessionErr := parseCanonicalUUID(payload.SessionID)
	credentialID, credentialErr := parseCanonicalUUID(payload.CredentialID)
	attemptID, attemptErr := parseCanonicalUUID(payload.VerificationAttemptID)
	if seriesErr != nil || instanceErr != nil || sessionErr != nil ||
		credentialErr != nil || attemptErr != nil {
		return OnsiteCheckinRecordRequest{},
			errx.NewBadRequest("invalid Checkin recording request body")
	}
	return OnsiteCheckinRecordRequest{
		SeriesID:              seriesID,
		InstanceID:            instanceID,
		SessionID:             sessionID,
		RegistrationID:        registrationID,
		CredentialID:          credentialID,
		VerificationAttemptID: attemptID,
	}, nil
}

type OnsiteCheckinVerificationResponse struct {
	Decision              checkin.VerificationDecision `json:"decision"`
	VerificationAttemptID string                       `json:"verification_attempt_id"`
	SeriesID              string                       `json:"series_id"`
	InstanceID            string                       `json:"instance_id"`
	SessionID             string                       `json:"session_id"`
	CanRecord             bool                         `json:"can_record"`
	RegistrationID        string                       `json:"registration_id,omitempty"`
	CredentialID          string                       `json:"credential_id,omitempty"`
	CheckinID             string                       `json:"checkin_id,omitempty"`
	OccurredAt            string                       `json:"occurred_at"`
	Replayed              bool                         `json:"replayed"`
}

func projectOnsiteCheckinVerificationResponse(
	result checkinpostgres.VerifyCredentialResult,
) OnsiteCheckinVerificationResponse {
	attempt := result.Attempt
	projected := OnsiteCheckinVerificationResponse{
		Decision:              attempt.Decision,
		VerificationAttemptID: attempt.ID.String(),
		SeriesID:              attempt.RequestedSeriesID.String(),
		InstanceID:            attempt.RequestedInstanceID.String(),
		SessionID:             attempt.RequestedSessionID.String(),
		CanRecord:             attempt.Decision == checkin.VerificationDecisionValid,
		OccurredAt:            attempt.OccurredAt.UTC().Format(time.RFC3339Nano),
		Replayed:              result.Duplicate,
	}
	if attempt.Decision == checkin.VerificationDecisionValid ||
		attempt.Decision == checkin.VerificationDecisionAlreadyCheckedIn {
		projected.RegistrationID = attempt.RegistrationID.String()
		projected.CredentialID = attempt.CredentialID.String()
	}
	if attempt.Decision == checkin.VerificationDecisionAlreadyCheckedIn {
		projected.CheckinID = attempt.CheckinID.String()
	}
	return projected
}

type OnsiteCheckinRecordResponse struct {
	CheckinID      string         `json:"checkin_id"`
	RegistrationID string         `json:"registration_id"`
	SeriesID       string         `json:"series_id"`
	InstanceID     string         `json:"instance_id"`
	SessionID      string         `json:"session_id"`
	Status         checkin.Status `json:"status"`
	CheckedInAt    string         `json:"checked_in_at"`
	Duplicate      bool           `json:"duplicate"`
}

func projectOnsiteCheckinRecordResponse(
	result checkinpostgres.RecordCheckinResult,
) OnsiteCheckinRecordResponse {
	value := result.Checkin
	return OnsiteCheckinRecordResponse{
		CheckinID:      value.ID.String(),
		RegistrationID: value.RegistrationID.String(),
		SeriesID:       value.SeriesID.String(),
		InstanceID:     value.InstanceID.String(),
		SessionID:      value.SessionID.String(),
		Status:         value.CheckinStatus,
		CheckedInAt:    value.CheckedInAt.UTC().Format(time.RFC3339Nano),
		Duplicate:      result.Duplicate,
	}
}

func decodeOnsiteCheckinJSON(c *gin.Context, target any) error {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxOnsiteCheckinBodyBytes,
	)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return errx.New(
				errx.CodeFileTooLarge,
				"Checkin request is too large",
			)
		}
		return errx.NewBadRequest("invalid Checkin request body")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errx.NewBadRequest("invalid Checkin request body")
	}
	return nil
}

func writeOnsiteCheckinError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidOnsiteCheckinRequest),
		errors.Is(err, checkinpostgres.ErrInvalidVerifyCredentialCommand),
		errors.Is(err, checkinpostgres.ErrInvalidRecordCheckinCommand):
		writeError(c, errx.NewBadRequest("invalid Checkin command"))
	case errors.Is(err, checkinpostgres.ErrCheckinOperatorForbidden):
		writeError(c, errx.NewForbidden("Checkin operator is not authorized"))
	case errors.Is(err, checkinpostgres.ErrCheckinVerificationTargetNotFound),
		errors.Is(err, checkinpostgres.ErrCheckinRecordTargetNotFound):
		writeError(c, errx.NewNotFound("Checkin target not found"))
	case errors.Is(err, checkinpostgres.ErrCheckinVerificationIdempotencyConflict),
		errors.Is(err, checkinpostgres.ErrCheckinVerificationTransactionConflict),
		errors.Is(err, checkinpostgres.ErrCheckinRecordUnavailable),
		errors.Is(err, checkinpostgres.ErrCheckinVerificationRejected),
		errors.Is(err, checkinpostgres.ErrCheckinRecordIdempotencyConflict),
		errors.Is(err, checkinpostgres.ErrCheckinRecordTransactionConflict),
		errors.Is(err, ErrOnsiteCheckinResponseConflict):
		writeError(c, errx.NewConflict("Checkin facts changed; verify and retry"))
	case errors.Is(err, checkinpostgres.ErrCheckinAuthorizationUnavailable):
		c.PureJSON(http.StatusServiceUnavailable, response.Body{
			Code:    int(errx.CodeInternal),
			Message: "service unavailable",
		})
	case errors.Is(err, checkinpostgres.ErrCheckinVerificationGenerationInactive),
		errors.Is(err, checkinpostgres.ErrCheckinRecordGenerationInactive):
		c.PureJSON(http.StatusServiceUnavailable, response.Body{
			Code:    int(errx.CodeInternal),
			Message: "service unavailable",
		})
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan Checkin command failed"))
	}
}
