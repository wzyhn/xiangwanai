package xiangwanapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

var (
	ErrInvalidSessionQuestionnaireService = errors.New(
		"invalid xiangwan Session questionnaire service",
	)
	ErrInvalidSessionQuestionnaireRequest = errors.New(
		"invalid xiangwan Session questionnaire request",
	)
	ErrQuestionnaireSessionNotSubmittable = errors.New(
		"xiangwan questionnaire Session is not submittable",
	)
	ErrSessionQuestionnaireConflict = errors.New(
		"xiangwan Session questionnaire identity conflict",
	)
)

type questionnaireSessionReader interface {
	ReadSessionDetail(
		context.Context,
		uuid.UUID,
	) (PublicSessionDetailPage, error)
}

type sessionQuestionnaireReader interface {
	ReadSessionQuestionnaire(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (activity.SessionQuestionnaire, error)
}

type SessionQuestionnaireService struct {
	tenantID            uuid.UUID
	sessionReader       questionnaireSessionReader
	questionnaireReader sessionQuestionnaireReader
}

func NewSessionQuestionnaireService(
	tenantID uuid.UUID,
	sessionReader questionnaireSessionReader,
	questionnaireReader sessionQuestionnaireReader,
) (*SessionQuestionnaireService, error) {
	if tenantID == uuid.Nil || sessionReader == nil || questionnaireReader == nil {
		return nil, ErrInvalidSessionQuestionnaireService
	}
	return &SessionQuestionnaireService{
		tenantID:            tenantID,
		sessionReader:       sessionReader,
		questionnaireReader: questionnaireReader,
	}, nil
}

func (service *SessionQuestionnaireService) Read(
	ctx context.Context,
	principalID uuid.UUID,
	sessionID uuid.UUID,
) (activity.SessionQuestionnaire, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.sessionReader == nil || service.questionnaireReader == nil {
		return activity.SessionQuestionnaire{},
			ErrInvalidSessionQuestionnaireService
	}
	if ctx == nil || principalID == uuid.Nil || sessionID == uuid.Nil {
		return activity.SessionQuestionnaire{},
			ErrInvalidSessionQuestionnaireRequest
	}
	page, err := service.sessionReader.ReadSessionDetail(ctx, sessionID)
	if err != nil {
		return activity.SessionQuestionnaire{}, fmt.Errorf(
			"read questionnaire Session eligibility: %w",
			err,
		)
	}
	if page.BrandStatus != activity.BrandLifecycleActive ||
		!page.Detail.CTA.Enabled ||
		page.Detail.CTA.Action != activity.SessionDetailCTAActionStartRegistration {
		return activity.SessionQuestionnaire{},
			ErrQuestionnaireSessionNotSubmittable
	}
	questionnaire, err := service.questionnaireReader.ReadSessionQuestionnaire(
		ctx,
		service.tenantID,
		sessionID,
	)
	if err != nil {
		return activity.SessionQuestionnaire{}, fmt.Errorf(
			"read current Session questionnaire: %w",
			err,
		)
	}
	if questionnaire.SessionID != sessionID ||
		questionnaire.InstanceID != page.Detail.InstanceID {
		return activity.SessionQuestionnaire{},
			ErrSessionQuestionnaireConflict
	}
	if err := activity.ValidateSessionQuestionnaire(questionnaire); err != nil {
		return activity.SessionQuestionnaire{}, fmt.Errorf(
			"validate current Session questionnaire: %w",
			err,
		)
	}
	return activity.CloneSessionQuestionnaire(questionnaire), nil
}
