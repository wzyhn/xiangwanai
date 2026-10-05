package xiangwanapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestSessionQuestionnaireServiceReturnsExactEligibleVersion(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	sessionID := uuid.New()
	instanceID := uuid.New()
	questionnaire := validAPIQuestionnaire(instanceID, sessionID)
	sessionReader := &fakeQuestionnaireSessionReader{page: PublicSessionDetailPage{
		BrandStatus: activity.BrandLifecycleActive,
		Detail: activity.SessionDetail{
			InstanceID: instanceID,
			SessionID:  sessionID,
			CTA: activity.SessionDetailCTA{
				Action:  activity.SessionDetailCTAActionStartRegistration,
				Enabled: true,
			},
		},
	}}
	questionnaireReader := &fakeSessionQuestionnaireReader{
		questionnaire: questionnaire,
	}
	service, err := NewSessionQuestionnaireService(
		tenantID,
		sessionReader,
		questionnaireReader,
	)
	if err != nil {
		t.Fatalf("NewSessionQuestionnaireService() error = %v", err)
	}

	got, err := service.Read(context.Background(), principalID, sessionID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got.QuestionnaireVersionID != questionnaire.QuestionnaireVersionID ||
		got.SessionID != sessionID || got.InstanceID != instanceID {
		t.Fatalf("Read() = %+v", got)
	}
	if sessionReader.calls != 1 || sessionReader.sessionID != sessionID ||
		questionnaireReader.calls != 1 ||
		questionnaireReader.tenantID != tenantID ||
		questionnaireReader.sessionID != sessionID {
		t.Fatalf(
			"readers session=%+v questionnaire=%+v",
			sessionReader,
			questionnaireReader,
		)
	}
	got.Fields[0].Label = "changed"
	if questionnaireReader.questionnaire.Fields[0].Label == "changed" {
		t.Fatal("Read() returned reader-owned mutable state")
	}
}

func TestSessionQuestionnaireServiceFailsClosed(t *testing.T) {
	t.Parallel()

	sessionID := uuid.New()
	instanceID := uuid.New()
	validPage := PublicSessionDetailPage{
		BrandStatus: activity.BrandLifecycleActive,
		Detail: activity.SessionDetail{
			InstanceID: instanceID,
			SessionID:  sessionID,
			CTA: activity.SessionDetailCTA{
				Action:  activity.SessionDetailCTAActionStartRegistration,
				Enabled: true,
			},
		},
	}
	tests := []struct {
		name                string
		principalID         uuid.UUID
		requestedSessionID  uuid.UUID
		sessionPage         PublicSessionDetailPage
		sessionErr          error
		questionnaire       activity.SessionQuestionnaire
		questionnaireErr    error
		wantErr             error
		wantQuestionnaireIO int
	}{
		{
			name:               "missing principal",
			requestedSessionID: sessionID,
			wantErr:            ErrInvalidSessionQuestionnaireRequest,
		},
		{
			name:               "missing Session",
			principalID:        uuid.New(),
			requestedSessionID: sessionID,
			sessionErr:         activity.ErrSessionDetailUnavailable,
			wantErr:            activity.ErrSessionDetailUnavailable,
		},
		{
			name:               "closed Session",
			principalID:        uuid.New(),
			requestedSessionID: sessionID,
			sessionPage: PublicSessionDetailPage{
				BrandStatus: activity.BrandLifecycleActive,
				Detail: activity.SessionDetail{
					InstanceID: instanceID,
					SessionID:  sessionID,
				},
			},
			wantErr: ErrQuestionnaireSessionNotSubmittable,
		},
		{
			name:                "questionnaire unavailable",
			principalID:         uuid.New(),
			requestedSessionID:  sessionID,
			sessionPage:         validPage,
			questionnaireErr:    activity.ErrQuestionnaireUnavailable,
			wantErr:             activity.ErrQuestionnaireUnavailable,
			wantQuestionnaireIO: 1,
		},
		{
			name:                "identity conflict",
			principalID:         uuid.New(),
			requestedSessionID:  sessionID,
			sessionPage:         validPage,
			questionnaire:       validAPIQuestionnaire(uuid.New(), sessionID),
			wantErr:             ErrSessionQuestionnaireConflict,
			wantQuestionnaireIO: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sessionReader := &fakeQuestionnaireSessionReader{
				page: test.sessionPage,
				err:  test.sessionErr,
			}
			questionnaireReader := &fakeSessionQuestionnaireReader{
				questionnaire: test.questionnaire,
				err:           test.questionnaireErr,
			}
			service, err := NewSessionQuestionnaireService(
				uuid.New(),
				sessionReader,
				questionnaireReader,
			)
			if err != nil {
				t.Fatalf("NewSessionQuestionnaireService() error = %v", err)
			}
			_, err = service.Read(
				context.Background(),
				test.principalID,
				test.requestedSessionID,
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Read() error = %v, want %v", err, test.wantErr)
			}
			if questionnaireReader.calls != test.wantQuestionnaireIO {
				t.Fatalf("questionnaire reader calls = %d", questionnaireReader.calls)
			}
		})
	}
}

type fakeQuestionnaireSessionReader struct {
	page PublicSessionDetailPage
	err  error

	calls     int
	sessionID uuid.UUID
}

func (reader *fakeQuestionnaireSessionReader) ReadSessionDetail(
	_ context.Context,
	sessionID uuid.UUID,
) (PublicSessionDetailPage, error) {
	reader.calls++
	reader.sessionID = sessionID
	return reader.page, reader.err
}

type fakeSessionQuestionnaireReader struct {
	questionnaire activity.SessionQuestionnaire
	err           error

	calls     int
	tenantID  uuid.UUID
	sessionID uuid.UUID
}

func (reader *fakeSessionQuestionnaireReader) ReadSessionQuestionnaire(
	_ context.Context,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
) (activity.SessionQuestionnaire, error) {
	reader.calls++
	reader.tenantID = tenantID
	reader.sessionID = sessionID
	return activity.CloneSessionQuestionnaire(reader.questionnaire), reader.err
}

func validAPIQuestionnaire(
	instanceID uuid.UUID,
	sessionID uuid.UUID,
) activity.SessionQuestionnaire {
	maxLength := 100
	return activity.SessionQuestionnaire{
		QuestionnaireVersionID: uuid.New(),
		InstanceID:             instanceID,
		SessionID:              sessionID,
		Version:                2,
		PrivacyPurpose:         "用于报名服务",
		PrivacyPolicyVersion:   "privacy-2",
		PublishedAt:            time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC),
		Fields: []activity.QuestionnaireField{
			{
				FieldID:   uuid.New(),
				Code:      "expectation",
				Type:      activity.QuestionnaireFieldSingleLine,
				Label:     "期待收获",
				SortOrder: 0,
				MaxLength: &maxLength,
				Options:   []activity.QuestionnaireOption{},
			},
		},
	}
}
