package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxHostApplicationSubmissionBodyBytes = 64 * 1024

type hostApplicationSubmissionApplication interface {
	Apply(
		context.Context,
		uuid.UUID,
		HostApplicationSubmissionRequest,
	) (peoplepostgres.HostApplicationResult, error)
}

type HostApplicationSubmissionHandler struct {
	service   hostApplicationSubmissionApplication
	principal PrincipalResolver
}

func NewHostApplicationSubmissionHandler(
	service hostApplicationSubmissionApplication,
	principal PrincipalResolver,
) *HostApplicationSubmissionHandler {
	return &HostApplicationSubmissionHandler{
		service:   service,
		principal: principal,
	}
}

func (handler *HostApplicationSubmissionHandler) RegisterRoutes(
	group *gin.RouterGroup,
) {
	group.POST("/me/host-applications", handler.Apply)
	group.POST("/me/host-applications/:application_id/withdrawal", handler.Withdraw)
}

// Apply godoc
// @Summary Submit the authenticated consumer's Xiangwan host application
// @Description BUSINESS-STATE command. The runtime derives Tenant and Principal, then PostgreSQL binds the submission to the current configured application cycle and policy version. A repeat in the same active cycle returns the existing application. Missing host rules and an existing host identity fail closed. Submitted contact and narratives are never echoed.
// @Tags xiangwan
// @Accept json
// @Produce json
// @Param request body HostApplicationSubmissionHTTPRequest true "Host application"
// @Security BearerAuth
// @Success 200 {object} HostApplicationSubmissionResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 413 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/host-applications [post]
func (handler *HostApplicationSubmissionHandler) Apply(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan Host Application is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || c.ContentType() != "application/json" ||
		len(c.Request.Header.Values("Idempotency-Key")) != 0 {
		writeError(c, errx.NewBadRequest("invalid Host Application request"))
		return
	}
	request, err := decodeHostApplicationSubmission(c)
	if err != nil {
		writeError(c, err)
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	result, err := handler.service.Apply(
		c.Request.Context(),
		principalID,
		request,
	)
	if err != nil {
		writeHostApplicationSubmissionError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, projectHostApplicationSubmissionResponse(result))
}

type HostApplicationSubmissionHTTPRequest struct {
	ExpectedCycle                string `json:"expected_cycle"`
	ExpectedPolicyVersion        string `json:"expected_policy_version"`
	ExpectedPrivacyPolicyVersion string `json:"expected_privacy_policy_version"`
	Consent                      bool   `json:"consent"`
	PersonalIntroduction         string `json:"personal_introduction"`
	RelevantExperience           string `json:"relevant_experience"`
	Availability                 string `json:"availability"`
	ContactMethod                string `json:"contact_method"`
}

type HostApplicationSubmissionResponse struct {
	Application MyHostApplicationResponse `json:"application"`
	Duplicate   bool                      `json:"duplicate"`
}

func decodeHostApplicationSubmission(
	c *gin.Context,
) (HostApplicationSubmissionRequest, error) {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxHostApplicationSubmissionBodyBytes,
	)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var payload HostApplicationSubmissionHTTPRequest
	if err := decoder.Decode(&payload); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return HostApplicationSubmissionRequest{}, errx.New(
				errx.CodeFileTooLarge,
				"Host Application request is too large",
			)
		}
		return HostApplicationSubmissionRequest{},
			errx.NewBadRequest("invalid Host Application request body")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return HostApplicationSubmissionRequest{},
			errx.NewBadRequest("invalid Host Application request body")
	}
	return HostApplicationSubmissionRequest{
		PersonalIntroduction: payload.PersonalIntroduction,
		RelevantExperience:   payload.RelevantExperience,
		Availability:         payload.Availability,
		ContactMethod:        payload.ContactMethod,
		ExpectedCycle:        payload.ExpectedCycle, ExpectedPolicyVersion: payload.ExpectedPolicyVersion, ExpectedPrivacyPolicyVersion: payload.ExpectedPrivacyPolicyVersion, Consent: payload.Consent,
	}, nil
}

func writeHostApplicationSubmissionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidHostApplicationSubmissionRequest),
		errors.Is(err, peoplepostgres.ErrInvalidHostApplicationCommand),
		errors.Is(err, people.ErrInvalidHostApplication):
		writeError(c, errx.NewBadRequest("invalid Host Application submission"))
	case errors.Is(err, peoplepostgres.ErrHostApplicationForbidden),
		errors.Is(err, peoplepostgres.ErrHostApplicationApplicantUnavailable):
		writeError(c, errx.NewForbidden("Host Application is unavailable"))
	case errors.Is(err, peoplepostgres.ErrHostApplicationRulesUnavailable):
		writeError(c, errx.NewConflict("Host Application rules are unavailable"))
	case errors.Is(err, peoplepostgres.ErrHostApplicationNotFound):
		writeError(c, errx.NewNotFound("Host Application not found"))
	case errors.Is(err, people.ErrHostApplicationTerminal):
		writeError(c, errx.NewConflict("Host Application has a terminal outcome"))
	case errors.Is(err, peoplepostgres.ErrHostApplicationAlreadyHost):
		writeError(c, errx.NewConflict("Host identity already exists"))
	case errors.Is(err, peoplepostgres.ErrHostApplicationTransactionConflict),
		errors.Is(err, peoplepostgres.ErrHostApplicationVersionConflict):
		writeError(c, errx.NewConflict("Host Application facts changed; refresh and retry"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan Host Application failed"))
	}
}

func projectHostApplicationSubmissionResponse(
	result peoplepostgres.HostApplicationResult,
) HostApplicationSubmissionResponse {
	application := result.Application
	projected := MyHostApplicationResponse{
		ApplicationID:     application.ID.String(),
		ApplicationCycle:  application.ApplicationCycle,
		PolicyVersion:     application.PolicyVersion,
		ApplicationStatus: application.ApplicationStatus,
		Version:           application.Version,
		SubmittedAt:       formatMyRegistrationTime(application.SubmittedAt),
		UpdatedAt:         formatMyRegistrationTime(application.UpdatedAt),
	}
	if application.ReviewComment != nil {
		projected.ReviewComment = *application.ReviewComment
	}
	return HostApplicationSubmissionResponse{
		Application: projected,
		Duplicate:   result.Duplicate,
	}
}

// Withdraw godoc
// @Summary Withdraw the authenticated consumer's pending host application
// @Tags xiangwan
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param application_id path string true "Application UUID"
// @Param request body HostApplicationWithdrawalRequest true "Current version"
// @Success 200 {object} HostApplicationSubmissionResponse
// @Router /xiangwan/me/host-applications/{application_id}/withdrawal [post]
func (handler *HostApplicationSubmissionHandler) Withdraw(c *gin.Context) {
	service, ok := handler.service.(interface {
		Withdraw(context.Context, uuid.UUID, uuid.UUID, int64) (peoplepostgres.HostApplicationResult, error)
	})
	if !ok {
		writeError(c, errx.NewInternal("Host Application is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || c.ContentType() != "application/json" || len(c.Request.Header.Values("Idempotency-Key")) != 0 {
		writeError(c, errx.NewBadRequest("invalid withdrawal"))
		return
	}
	id, err := uuid.Parse(c.Param("application_id"))
	if err != nil || id == uuid.Nil {
		writeError(c, errx.NewBadRequest("invalid application"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	var payload HostApplicationWithdrawalRequest
	if d.Decode(&payload) != nil || payload.ExpectedVersion < 1 || d.Decode(new(any)) != io.EOF {
		writeError(c, errx.NewBadRequest("invalid withdrawal"))
		return
	}
	owner, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	result, err := service.Withdraw(c.Request.Context(), owner, id, payload.ExpectedVersion)
	if err != nil {
		writeHostApplicationSubmissionError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, projectHostApplicationSubmissionResponse(result))
}

type HostApplicationWithdrawalRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}
