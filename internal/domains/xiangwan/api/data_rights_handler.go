package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights"
	datarightspostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxDataRightsSubmissionBodyBytes = 4 * 1024

type dataRightsApplication interface {
	Submit(
		context.Context,
		uuid.UUID,
		DataRightsSubmissionRequest,
	) (datarightspostgres.SubmissionResult, error)
	ListMine(context.Context, uuid.UUID) ([]datarights.CaseHistory, error)
}

type DataRightsHandler struct {
	service   dataRightsApplication
	principal PrincipalResolver
}

func NewDataRightsHandler(
	service dataRightsApplication,
	principal PrincipalResolver,
) *DataRightsHandler {
	return &DataRightsHandler{service: service, principal: principal}
}

func (handler *DataRightsHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/me/data-rights-requests", handler.GetMine)
	group.POST("/me/data-rights-requests", handler.Submit)
}

// Submit godoc
// @Summary Submit the authenticated consumer's Xiangwan data-rights request
// @Description OP-KEY command. Creates an owner-scoped access, correction, export, or deletion review case under the current server-owned privacy-policy version. A deletion request never promises synchronous or unconditional physical deletion. Operation keys and evidence do not cross the response.
// @Tags xiangwan
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body DataRightsSubmissionHTTPRequest true "Data-rights request"
// @Security BearerAuth
// @Success 200 {object} DataRightsSubmissionResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 413 {object} response.Body
// @Failure 500 {object} response.Body
// @Failure 503 {object} response.Body
// @Router /xiangwan/me/data-rights-requests [post]
func (handler *DataRightsHandler) Submit(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan Data Rights is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || c.ContentType() != "application/json" {
		writeError(c, errx.NewBadRequest("invalid Data Rights request"))
		return
	}
	operationKey, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	request, err := decodeDataRightsSubmission(c)
	if err != nil {
		writeError(c, err)
		return
	}
	request.OperationKey = operationKey
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	result, err := handler.service.Submit(
		c.Request.Context(),
		principalID,
		request,
	)
	if err != nil {
		writeDataRightsError(c, err)
		return
	}
	response.OK(c, DataRightsSubmissionResponse{
		Case:     projectDataRightsCase(result.Case),
		Replayed: result.Replayed,
	})
}

// GetMine godoc
// @Summary List the authenticated consumer's Xiangwan data-rights cases
// @Description Returns at most 100 owner-scoped cases with safe lifecycle, policy-basis, and delivery summaries. Operation keys, fingerprints, staff identities, evidence digests, and delivery destinations are never exposed.
// @Tags xiangwan
// @Produce json
// @Security BearerAuth
// @Success 200 {object} MyDataRightsCasesResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/data-rights-requests [get]
func (handler *DataRightsHandler) GetMine(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan Data Rights is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || c.Request.ContentLength != 0 ||
		len(c.Request.TransferEncoding) != 0 || c.GetHeader("Content-Type") != "" ||
		len(c.Request.Header.Values("Idempotency-Key")) != 0 {
		writeError(c, errx.NewBadRequest("invalid Data Rights request"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	histories, err := handler.service.ListMine(
		c.Request.Context(),
		principalID,
	)
	if err != nil {
		writeDataRightsError(c, err)
		return
	}
	response.OK(c, projectMyDataRightsCases(histories))
}

type DataRightsSubmissionHTTPRequest struct {
	RequestType  datarights.RequestType  `json:"request_type"`
	RequestScope datarights.RequestScope `json:"request_scope"`
}

type DataRightsSubmissionResponse struct {
	Case     DataRightsCaseResponse `json:"case"`
	Replayed bool                   `json:"replayed"`
}

type MyDataRightsCasesResponse struct {
	Items      []DataRightsCaseHistoryResponse `json:"items"`
	EmptyState string                          `json:"empty_state,omitempty"`
}

type DataRightsCaseHistoryResponse struct {
	Case       DataRightsCaseResponse       `json:"case"`
	Timeline   []DataRightsEventResponse    `json:"timeline"`
	Deliveries []DataRightsDeliveryResponse `json:"deliveries"`
}

type DataRightsCaseResponse struct {
	CaseID                   string                  `json:"case_id"`
	RequestType              datarights.RequestType  `json:"request_type"`
	RequestScope             datarights.RequestScope `json:"request_scope"`
	PrivacyPolicyVersion     string                  `json:"privacy_policy_version"`
	Status                   datarights.CaseStatus   `json:"status"`
	Version                  int64                   `json:"version"`
	SubmittedAt              string                  `json:"submitted_at"`
	UpdatedAt                string                  `json:"updated_at"`
	CompletedAt              string                  `json:"completed_at,omitempty"`
	PhysicalDeletionPromised bool                    `json:"physical_deletion_promised"`
}

type DataRightsEventResponse struct {
	EventType          datarights.EventType  `json:"event_type"`
	ResultingStatus    datarights.CaseStatus `json:"resulting_status"`
	PolicyBasisVersion string                `json:"policy_basis_version,omitempty"`
	OccurredAt         string                `json:"occurred_at"`
}

type DataRightsDeliveryResponse struct {
	Kind       datarights.DeliveryKind   `json:"kind"`
	Status     datarights.DeliveryStatus `json:"status"`
	OccurredAt string                    `json:"occurred_at"`
}

func decodeDataRightsSubmission(
	c *gin.Context,
) (DataRightsSubmissionRequest, error) {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxDataRightsSubmissionBodyBytes,
	)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var payload DataRightsSubmissionHTTPRequest
	if err := decoder.Decode(&payload); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return DataRightsSubmissionRequest{}, errx.New(
				errx.CodeFileTooLarge,
				"Data Rights request is too large",
			)
		}
		return DataRightsSubmissionRequest{},
			errx.NewBadRequest("invalid Data Rights request body")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return DataRightsSubmissionRequest{},
			errx.NewBadRequest("invalid Data Rights request body")
	}
	if !datarights.ValidRequestType(payload.RequestType) ||
		!datarights.ValidRequestScope(payload.RequestScope) {
		return DataRightsSubmissionRequest{},
			errx.NewBadRequest("invalid Data Rights request body")
	}
	return DataRightsSubmissionRequest{
		RequestType:  payload.RequestType,
		RequestScope: payload.RequestScope,
	}, nil
}

func writeDataRightsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidDataRightsRequest),
		errors.Is(err, datarights.ErrInvalidSubmission),
		errors.Is(err, datarightspostgres.ErrInvalidSubmissionCommand):
		writeError(c, errx.NewBadRequest("invalid Data Rights request"))
	case errors.Is(err, ErrDataRightsPolicyUnavailable):
		c.PureJSON(http.StatusServiceUnavailable, response.Body{
			Code:    int(errx.CodeInternal),
			Message: "service unavailable",
		})
	case errors.Is(err, datarightspostgres.ErrDataRightsPrincipalUnavailable):
		writeError(c, errx.NewForbidden("Data Rights request is unavailable"))
	case errors.Is(err, datarightspostgres.ErrDataRightsOperationConflict):
		writeError(c, errx.NewConflict("Idempotency-Key conflicts with another request"))
	case errors.Is(err, datarightspostgres.ErrDataRightsTransactionConflict):
		writeError(c, errx.NewConflict("Data Rights facts changed; retry"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan Data Rights failed"))
	}
}

func projectDataRightsCase(value datarights.Case) DataRightsCaseResponse {
	result := DataRightsCaseResponse{
		CaseID:               value.ID.String(),
		RequestType:          value.RequestType,
		RequestScope:         value.RequestScope,
		PrivacyPolicyVersion: value.PrivacyPolicyVersion,
		Status:               value.Status,
		Version:              value.Version,
		SubmittedAt:          formatMyRegistrationTime(value.SubmittedAt),
		UpdatedAt:            formatMyRegistrationTime(value.UpdatedAt),
	}
	if value.CompletedAt != nil {
		result.CompletedAt = formatMyRegistrationTime(*value.CompletedAt)
	}
	return result
}

func projectMyDataRightsCases(
	histories []datarights.CaseHistory,
) MyDataRightsCasesResponse {
	result := MyDataRightsCasesResponse{
		Items: make([]DataRightsCaseHistoryResponse, 0, len(histories)),
	}
	for _, history := range histories {
		item := DataRightsCaseHistoryResponse{
			Case:       projectDataRightsCase(history.Case),
			Timeline:   make([]DataRightsEventResponse, 0, len(history.Events)),
			Deliveries: []DataRightsDeliveryResponse{},
		}
		for _, event := range history.Events {
			item.Timeline = append(item.Timeline, DataRightsEventResponse{
				EventType:          event.EventType,
				ResultingStatus:    event.ResultingStatus,
				PolicyBasisVersion: event.PolicyBasisVersion,
				OccurredAt:         formatMyRegistrationTime(event.OccurredAt),
			})
			if event.DeliveryKind != "" {
				item.Deliveries = append(item.Deliveries, DataRightsDeliveryResponse{
					Kind:       event.DeliveryKind,
					Status:     event.DeliveryStatus,
					OccurredAt: formatMyRegistrationTime(event.OccurredAt),
				})
			}
		}
		result.Items = append(result.Items, item)
	}
	if len(result.Items) == 0 {
		result.EmptyState = "no_data_rights_requests"
	}
	return result
}
