package xiangwanapi

import (
	"context"
	"errors"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type sessionQuestionnaireApplication interface {
	Read(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (activity.SessionQuestionnaire, error)
}

type SessionQuestionnaireHandler struct {
	service   sessionQuestionnaireApplication
	principal PrincipalResolver
}

func NewSessionQuestionnaireHandler(
	service sessionQuestionnaireApplication,
	principal PrincipalResolver,
) *SessionQuestionnaireHandler {
	return &SessionQuestionnaireHandler{
		service:   service,
		principal: principal,
	}
}

func (handler *SessionQuestionnaireHandler) RegisterRoutes(
	group *gin.RouterGroup,
) {
	group.GET(
		"/sessions/:session_id/questionnaire",
		handler.GetSessionQuestionnaire,
	)
	group.GET("/me/questionnaire-prefill/:session_id", handler.GetQuestionnairePrefill)
}

// GetSessionQuestionnaire godoc
// @Summary Read the current submittable Xiangwan questionnaire
// @Description Returns the exact published Instance questionnaire for an authenticated consumer and an eligible Session. When no questionnaire is assigned, returns 200 with available=false and the exact session_id.
// @Tags xiangwan
// @Produce json
// @Param session_id path string true "Session ID"
// @Security BearerAuth
// @Success 200 {object} SessionQuestionnaireResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/sessions/{session_id}/questionnaire [get]
func (handler *SessionQuestionnaireHandler) GetSessionQuestionnaire(
	c *gin.Context,
) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan Session questionnaire is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		writeError(c, errx.NewBadRequest("invalid Session questionnaire query"))
		return
	}
	sessionID, err := parseCanonicalUUID(c.Param("session_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Session id"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	questionnaire, err := handler.service.Read(
		c.Request.Context(),
		principalID,
		sessionID,
	)
	if errors.Is(err, activity.ErrQuestionnaireUnavailable) {
		response.OK(c, SessionQuestionnaireResponse{
			SessionID: sessionID.String(),
			Fields:    []SessionQuestionnaireFieldResponse{},
		})
		return
	}
	if err != nil {
		writeSessionQuestionnaireError(c, err)
		return
	}
	response.OK(c, projectSessionQuestionnaireResponse(questionnaire))
}

func writeSessionQuestionnaireError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidSessionQuestionnaireRequest):
		writeError(c, errx.NewBadRequest("invalid Session questionnaire request"))
	case errors.Is(err, activity.ErrQuestionnaireUnavailable),
		errors.Is(err, activity.ErrSessionDetailUnavailable):
		writeError(c, errx.NewNotFound("questionnaire not found"))
	case errors.Is(err, ErrQuestionnaireSessionNotSubmittable):
		writeError(c, errx.NewConflict("Session is not open for registration"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan Session questionnaire failed"))
	}
}

type SessionQuestionnaireResponse struct {
	Available              bool                                `json:"available"`
	QuestionnaireVersionID string                              `json:"questionnaire_version_id"`
	InstanceID             string                              `json:"instance_id"`
	SessionID              string                              `json:"session_id"`
	Version                int64                               `json:"version"`
	PrivacyPurpose         string                              `json:"privacy_purpose"`
	PrivacyPolicyVersion   string                              `json:"privacy_policy_version"`
	PublishedAt            string                              `json:"published_at"`
	Fields                 []SessionQuestionnaireFieldResponse `json:"fields"`
}

type SessionQuestionnaireFieldResponse struct {
	FieldID       string                          `json:"field_id"`
	Code          string                          `json:"code"`
	Type          activity.QuestionnaireFieldType `json:"type"`
	Label         string                          `json:"label"`
	HelpText      string                          `json:"help_text"`
	Required      bool                            `json:"required"`
	SortOrder     int                             `json:"sort_order"`
	MinLength     *int                            `json:"min_length"`
	MaxLength     *int                            `json:"max_length"`
	MaxSelections *int                            `json:"max_selections"`
	Options       []QuestionnaireOptionResponse   `json:"options"`
}

type QuestionnaireOptionResponse struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

func projectSessionQuestionnaireResponse(
	questionnaire activity.SessionQuestionnaire,
) SessionQuestionnaireResponse {
	fields := make(
		[]SessionQuestionnaireFieldResponse,
		0,
		len(questionnaire.Fields),
	)
	for _, field := range questionnaire.Fields {
		options := make(
			[]QuestionnaireOptionResponse,
			0,
			len(field.Options),
		)
		for _, option := range field.Options {
			options = append(options, QuestionnaireOptionResponse{
				Code: option.Code, Label: option.Label,
			})
		}
		fields = append(fields, SessionQuestionnaireFieldResponse{
			FieldID:       field.FieldID.String(),
			Code:          field.Code,
			Type:          field.Type,
			Label:         field.Label,
			HelpText:      field.HelpText,
			Required:      field.Required,
			SortOrder:     field.SortOrder,
			MinLength:     cloneQuestionnaireResponseInt(field.MinLength),
			MaxLength:     cloneQuestionnaireResponseInt(field.MaxLength),
			MaxSelections: cloneQuestionnaireResponseInt(field.MaxSelections),
			Options:       options,
		})
	}
	return SessionQuestionnaireResponse{
		Available:              true,
		QuestionnaireVersionID: questionnaire.QuestionnaireVersionID.String(),
		InstanceID:             questionnaire.InstanceID.String(),
		SessionID:              questionnaire.SessionID.String(),
		Version:                questionnaire.Version,
		PrivacyPurpose:         questionnaire.PrivacyPurpose,
		PrivacyPolicyVersion:   questionnaire.PrivacyPolicyVersion,
		PublishedAt:            questionnaire.PublishedAt.UTC().Format(time.RFC3339Nano),
		Fields:                 fields,
	}
}

func cloneQuestionnaireResponseInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
