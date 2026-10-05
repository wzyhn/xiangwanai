package xiangwanapi

import (
	"context"
	"errors"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type QuestionnairePrefillResponse struct {
	SessionID              string                       `json:"session_id"`
	QuestionnaireVersionID string                       `json:"questionnaire_version_id"`
	Answers                []QuestionnairePrefillAnswer `json:"answers"`
}

type QuestionnairePrefillAnswer struct {
	FieldID string   `json:"field_id"`
	Values  []string `json:"values"`
}

type questionnairePrefillReader interface {
	ReadQuestionnairePrefill(context.Context, uuid.UUID, uuid.UUID, activity.SessionQuestionnaire) ([]activity.QuestionnaireAnswer, error)
}

func (service *SessionQuestionnaireService) ReadPrefill(ctx context.Context, principalID, sessionID uuid.UUID) (QuestionnairePrefillResponse, error) {
	result := QuestionnairePrefillResponse{SessionID: sessionID.String(), Answers: []QuestionnairePrefillAnswer{}}
	questionnaire, err := service.Read(ctx, principalID, sessionID)
	if errors.Is(err, activity.ErrQuestionnaireUnavailable) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.QuestionnaireVersionID = questionnaire.QuestionnaireVersionID.String()
	reader, ok := service.questionnaireReader.(questionnairePrefillReader)
	if !ok {
		return result, ErrInvalidSessionQuestionnaireService
	}
	answers, err := reader.ReadQuestionnairePrefill(ctx, service.tenantID, principalID, questionnaire)
	if err != nil {
		return result, err
	}
	if len(answers) == 0 {
		return result, nil
	}
	answers, err = activity.NormalizeQuestionnaireAnswers(questionnaire, answers)
	if err != nil {
		return result, err
	}
	for _, answer := range answers {
		values := append([]string{}, answer.Values...)
		result.Answers = append(result.Answers, QuestionnairePrefillAnswer{FieldID: answer.FieldID.String(), Values: values})
	}
	return result, nil
}

// GetQuestionnairePrefill godoc
// @Summary Reuse the consumer's equivalent questionnaire answers within one Series
// @Description Authenticated owner-only prefill. Only owner submissions with identical field definitions, purpose and privacy version are reused. Current field IDs are returned; consent and new Registration snapshots remain required. Responses must not be cached.
// @Tags xiangwan
// @Produce json
// @Param session_id path string true "Current Session ID"
// @Security BearerAuth
// @Success 200 {object} QuestionnairePrefillResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/questionnaire-prefill/{session_id} [get]
func (handler *SessionQuestionnaireHandler) GetQuestionnairePrefill(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("questionnaire prefill unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		writeError(c, errx.NewBadRequest("invalid questionnaire prefill query"))
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
	service, ok := handler.service.(interface {
		ReadPrefill(context.Context, uuid.UUID, uuid.UUID) (QuestionnairePrefillResponse, error)
	})
	if !ok {
		writeError(c, errx.NewInternal("questionnaire prefill unavailable"))
		return
	}
	result, err := service.ReadPrefill(c.Request.Context(), principalID, sessionID)
	if err != nil {
		writeSessionQuestionnaireError(c, err)
		return
	}
	response.OK(c, result)
}
