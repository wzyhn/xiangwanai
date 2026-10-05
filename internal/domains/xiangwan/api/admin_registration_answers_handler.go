package xiangwanapi

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// GetRegistrationAnswers godoc
// @Summary Read one Xiangwan Registration's questionnaire answers
// @Description Requires a live tenant activity-operator grant, an exact Registration ID, and an explicit purpose. Raw answer values are returned only in this single-record response; a content-free read audit commits before disclosure.
// @Tags xiangwan-admin
// @Produce json
// @Param registration_id path string true "Registration ID"
// @Param purpose query string true "Answer-read purpose" Enums(activity_coordination,event_followup)
// @Success 200 {object} AdminRegistrationAnswerDetailSetResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Router /xiangwan/admin/registrations/{registration_id}/answers [get]
func (handler *AdminCatalogHandler) GetRegistrationAnswers(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c, "purpose") {
		return
	}
	catalog, ok := handler.catalog.(xiangwanadmin.RegistrationAnswerDetailCatalog)
	if !ok {
		writeError(c, fmt.Errorf("administrator questionnaire answer detail catalog is unavailable"))
		return
	}
	registrationID, err := parseCanonicalUUID(c.Param("registration_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Registration id"))
		return
	}
	purpose := c.Query("purpose")
	if purpose != "activity_coordination" && purpose != "event_followup" {
		writeError(c, errx.NewBadRequest("invalid questionnaire answer read purpose"))
		return
	}
	value, err := catalog.GetRegistrationAnswers(c.Request.Context(), principal, registrationID, purpose)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminRegistrationAnswerDetailSet(value))
}

type AdminRegistrationAnswerOptionResponse struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

type AdminRegistrationAnswerDetailItemResponse struct {
	QuestionnaireVersionID string                                  `json:"questionnaire_version_id"`
	FieldID                string                                  `json:"field_id"`
	FieldCode              string                                  `json:"field_code"`
	FieldType              string                                  `json:"field_type"`
	FieldLabel             string                                  `json:"field_label"`
	Required               bool                                    `json:"required"`
	SortOrder              int                                     `json:"sort_order"`
	Options                []AdminRegistrationAnswerOptionResponse `json:"options"`
	AnswerValues           []string                                `json:"answer_values"`
	CreatedAt              string                                  `json:"created_at"`
}

type AdminRegistrationAnswerDetailSetResponse struct {
	RegistrationID string                                      `json:"registration_id"`
	InstanceID     string                                      `json:"instance_id"`
	SessionID      string                                      `json:"session_id"`
	Items          []AdminRegistrationAnswerDetailItemResponse `json:"items"`
}

func projectAdminRegistrationAnswerDetailSet(
	value xiangwanadmin.RegistrationAnswerDetailSet,
) AdminRegistrationAnswerDetailSetResponse {
	items := make([]AdminRegistrationAnswerDetailItemResponse, 0, len(value.Items))
	for _, item := range value.Items {
		options := make([]AdminRegistrationAnswerOptionResponse, 0, len(item.Options))
		for _, option := range item.Options {
			options = append(options, AdminRegistrationAnswerOptionResponse{Code: option.Code, Label: option.Label})
		}
		items = append(items, AdminRegistrationAnswerDetailItemResponse{
			QuestionnaireVersionID: item.QuestionnaireVersionID.String(),
			FieldID:                item.FieldID.String(), FieldCode: item.FieldCode,
			FieldType: string(item.FieldType), FieldLabel: item.FieldLabel,
			Required: item.Required, SortOrder: item.SortOrder, Options: options,
			AnswerValues: append([]string{}, item.Values...),
			CreatedAt:    item.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return AdminRegistrationAnswerDetailSetResponse{
		RegistrationID: value.RegistrationID.String(),
		InstanceID:     value.InstanceID.String(), SessionID: value.SessionID.String(),
		Items: items,
	}
}

// ListRegistrationAnswerSummaries godoc
// @Summary List privacy-preserving Xiangwan questionnaire answer summaries
// @Description Reads only answered/value-count metadata for a filtered Instance, Session, or Registration. Raw answer values and contact snapshots are never returned. Activity-operator authorization, pagination, and a read audit event are enforced by the PostgreSQL adapter.
// @Tags xiangwan-admin
// @Produce json text/csv
// @Param instance_id query string false "Exact Instance ID (at least one filter is required)"
// @Param session_id query string false "Exact Session ID (at least one filter is required)"
// @Param registration_id query string false "Exact Registration ID (at least one filter is required)"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param format query string false "Response format" Enums(json,csv)
// @Success 200 {object} AdminRegistrationAnswerSummaryPageResponse
// @Success 200 {string} string "CSV when format=csv"
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Router /xiangwan/admin/registrations/answer-summaries [get]
func (handler *AdminCatalogHandler) ListRegistrationAnswerSummaries(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c,
		"instance_id", "session_id", "registration_id", "page", "page_size", "format",
	) {
		return
	}
	catalog, ok := handler.catalog.(xiangwanadmin.RegistrationAnswerCatalog)
	if !ok {
		writeError(c, fmt.Errorf("administrator questionnaire answer catalog is unavailable"))
		return
	}
	page, pageSize, ok := parseAdminPage(c)
	if !ok {
		return
	}
	instanceID, ok := parseOptionalAdminUUID(c, "instance_id")
	if !ok {
		return
	}
	sessionID, ok := parseOptionalAdminUUID(c, "session_id")
	if !ok {
		return
	}
	registrationID, ok := parseOptionalAdminUUID(c, "registration_id")
	if !ok {
		return
	}
	if instanceID == nil && sessionID == nil && registrationID == nil {
		writeError(c, errx.NewBadRequest("one answer summary filter is required"))
		return
	}
	format := c.Query("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		writeError(c, errx.NewBadRequest("invalid questionnaire answer summary format"))
		return
	}
	result, err := catalog.ListRegistrationAnswerSummaries(
		c.Request.Context(), principal,
		xiangwanadmin.RegistrationAnswerSummaryFilter{
			InstanceID: instanceID, SessionID: sessionID, RegistrationID: registrationID,
			Page: page, PageSize: pageSize, Format: format,
		},
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	if format == "csv" {
		writeRegistrationAnswerSummaryCSV(c, result)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	response.OK(c, projectAdminRegistrationAnswerSummaryPage(result))
}

type AdminRegistrationAnswerSummaryResponse struct {
	RegistrationID         string `json:"registration_id"`
	InstanceID             string `json:"instance_id"`
	SessionID              string `json:"session_id"`
	QuestionnaireVersionID string `json:"questionnaire_version_id"`
	FieldID                string `json:"field_id"`
	FieldCode              string `json:"field_code"`
	FieldType              string `json:"field_type"`
	FieldLabel             string `json:"field_label"`
	Required               bool   `json:"required"`
	SortOrder              int    `json:"sort_order"`
	Answered               bool   `json:"answered"`
	ValueCount             int    `json:"value_count"`
	CreatedAt              string `json:"created_at"`
}

type AdminRegistrationAnswerSummaryPageResponse struct {
	Items    []AdminRegistrationAnswerSummaryResponse `json:"items"`
	Page     int                                      `json:"page"`
	PageSize int                                      `json:"page_size"`
	Total    int64                                    `json:"total"`
}

func projectAdminRegistrationAnswerSummaryPage(
	value xiangwanadmin.RegistrationAnswerSummaryPage,
) AdminRegistrationAnswerSummaryPageResponse {
	items := make([]AdminRegistrationAnswerSummaryResponse, 0, len(value.Items))
	for _, item := range value.Items {
		items = append(items, AdminRegistrationAnswerSummaryResponse{
			RegistrationID:         item.RegistrationID.String(),
			InstanceID:             item.InstanceID.String(),
			SessionID:              item.SessionID.String(),
			QuestionnaireVersionID: item.QuestionnaireVersionID.String(),
			FieldID:                item.FieldID.String(),
			FieldCode:              item.FieldCode,
			FieldType:              string(item.FieldType),
			FieldLabel:             item.FieldLabel,
			Required:               item.Required,
			SortOrder:              item.SortOrder,
			Answered:               item.Answered,
			ValueCount:             item.ValueCount,
			CreatedAt:              item.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return AdminRegistrationAnswerSummaryPageResponse{
		Items: items, Page: value.Page, PageSize: value.PageSize, Total: value.Total,
	}
}

func writeRegistrationAnswerSummaryCSV(
	c *gin.Context,
	value xiangwanadmin.RegistrationAnswerSummaryPage,
) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(
		"attachment; filename=xiangwan-registration-answer-summary-page-%d.csv", value.Page,
	))
	c.Header("X-Xiangwan-Page", strconv.Itoa(value.Page))
	c.Header("X-Xiangwan-Page-Size", strconv.Itoa(value.PageSize))
	c.Header("X-Xiangwan-Total", strconv.FormatInt(value.Total, 10))
	c.Status(http.StatusOK)
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{
		"registration_id", "instance_id", "session_id", "questionnaire_version_id",
		"field_id", "field_code", "field_type", "field_label", "required",
		"sort_order", "answered", "value_count", "created_at",
	})
	for _, item := range value.Items {
		_ = writer.Write([]string{
			item.RegistrationID.String(), item.InstanceID.String(), item.SessionID.String(),
			item.QuestionnaireVersionID.String(), item.FieldID.String(), item.FieldCode,
			string(item.FieldType), item.FieldLabel, strconv.FormatBool(item.Required),
			strconv.Itoa(item.SortOrder), strconv.FormatBool(item.Answered),
			strconv.Itoa(item.ValueCount), item.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		// Headers have already been sent; surface the transport error to Gin for
		// request logging without attempting to append a JSON error body.
		_ = c.Error(err)
	}
}
