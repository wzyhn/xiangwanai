package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestReadSessionQuestionnaireUsesLatestTenantScopedAssignment(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	sessionID := uuid.New()
	instanceID := uuid.New()
	versionID := uuid.New()
	publishedAt := time.Date(2026, 9, 13, 2, 0, 0, 0, time.UTC)
	maxSelections := 2
	want := activity.SessionQuestionnaire{
		QuestionnaireVersionID: versionID,
		InstanceID:             instanceID,
		SessionID:              sessionID,
		Version:                4,
		PrivacyPurpose:         "用于活动报名与现场服务",
		PrivacyPolicyVersion:   "privacy-4",
		PublishedAt:            publishedAt,
		Fields: []activity.QuestionnaireField{
			{
				FieldID:       uuid.New(),
				Code:          "topics",
				Type:          activity.QuestionnaireFieldMultipleChoice,
				Label:         "感兴趣的话题",
				HelpText:      "最多两项",
				Required:      true,
				SortOrder:     10,
				MaxSelections: &maxSelections,
				Options: []activity.QuestionnaireOption{
					{Code: "agents", Label: "智能体"},
					{Code: "product", Label: "产品"},
				},
			},
		},
	}
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRows{rows: [][]any{
				questionnaireScanValues(want, want.Fields[0]),
			}}, nil
		},
	}}

	got, err := repository.ReadSessionQuestionnaire(
		context.Background(),
		tenantID,
		sessionID,
	)
	if err != nil {
		t.Fatalf("ReadSessionQuestionnaire() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadSessionQuestionnaire() = %+v, want %+v", got, want)
	}
	for _, fragment := range []string{
		"activity_session.tenant_id = $1",
		"activity_session.id = $2",
		"assignment.tenant_id = activity_instance.tenant_id",
		"ORDER BY assignment.assignment_version DESC",
		"activity_series.current_public_instance_id = activity_instance.id",
		"brand_profile.lifecycle_status = 'active'",
		"questionnaire.status = 'published'",
		"ORDER BY field.sort_order, field.field_id",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("questionnaire query missing %q: %s", fragment, capturedQuery)
		}
	}
	if !reflect.DeepEqual(capturedArgs, []any{tenantID, sessionID}) {
		t.Fatalf("questionnaire query args = %#v", capturedArgs)
	}
}

func TestReadSessionQuestionnaireFailsClosed(t *testing.T) {
	t.Parallel()

	queryFailure := errors.New("query failure")
	iterationFailure := errors.New("iteration failure")
	tests := []struct {
		name     string
		executor *fakeQueryExecutor
		wantErr  error
	}{
		{
			name: "missing",
			executor: &fakeQueryExecutor{queryRows: func(
				string,
				...any,
			) (rowsScanner, error) {
				return &fakeRows{}, nil
			}},
			wantErr: activity.ErrQuestionnaireUnavailable,
		},
		{
			name: "query failure",
			executor: &fakeQueryExecutor{queryRows: func(
				string,
				...any,
			) (rowsScanner, error) {
				return nil, queryFailure
			}},
			wantErr: queryFailure,
		},
		{
			name: "iteration failure",
			executor: &fakeQueryExecutor{queryRows: func(
				string,
				...any,
			) (rowsScanner, error) {
				return &fakeRows{err: iterationFailure}, nil
			}},
			wantErr: iterationFailure,
		},
		{
			name: "invalid options",
			executor: &fakeQueryExecutor{queryRows: func(
				string,
				...any,
			) (rowsScanner, error) {
				questionnaire := validRepositoryQuestionnaire()
				row := questionnaireScanValues(
					questionnaire,
					questionnaire.Fields[0],
				)
				row[len(row)-1] = []byte(`{"code":"not-an-array"}`)
				return &fakeRows{rows: [][]any{row}}, nil
			}},
			wantErr: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := (&Repository{db: test.executor}).ReadSessionQuestionnaire(
				context.Background(),
				uuid.New(),
				uuid.New(),
			)
			if test.name == "invalid options" {
				if err == nil || !strings.Contains(err.Error(), "decode questionnaire options") {
					t.Fatalf("ReadSessionQuestionnaire() error = %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ReadSessionQuestionnaire() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func validRepositoryQuestionnaire() activity.SessionQuestionnaire {
	maxLength := 100
	return activity.SessionQuestionnaire{
		QuestionnaireVersionID: uuid.New(),
		InstanceID:             uuid.New(),
		SessionID:              uuid.New(),
		Version:                1,
		PrivacyPurpose:         "报名服务",
		PrivacyPolicyVersion:   "privacy-1",
		PublishedAt:            time.Now().UTC(),
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

func questionnaireScanValues(
	questionnaire activity.SessionQuestionnaire,
	field activity.QuestionnaireField,
) []any {
	return []any{
		questionnaire.QuestionnaireVersionID,
		questionnaire.InstanceID,
		questionnaire.SessionID,
		questionnaire.Version,
		questionnaire.PrivacyPurpose,
		questionnaire.PrivacyPolicyVersion,
		questionnaire.PublishedAt,
		field.FieldID,
		field.Code,
		field.Type,
		field.Label,
		field.HelpText,
		field.Required,
		field.SortOrder,
		questionnaireNullInt(field.MinLength),
		questionnaireNullInt(field.MaxLength),
		questionnaireNullInt(field.MaxSelections),
		questionnaireOptionsJSON(field.Options),
	}
}

func questionnaireNullInt(value *int) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*value), Valid: true}
}

func questionnaireOptionsJSON(options []activity.QuestionnaireOption) []byte {
	if len(options) == 0 {
		return []byte(`[]`)
	}
	return []byte(
		`[{"code":"agents","label":"智能体"},` +
			`{"code":"product","label":"产品"}]`,
	)
}
