package activity

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateSessionQuestionnaireAcceptsCanonicalFieldTypes(t *testing.T) {
	t.Parallel()

	questionnaire := validSessionQuestionnaireFixture()
	if err := ValidateSessionQuestionnaire(questionnaire); err != nil {
		t.Fatalf("ValidateSessionQuestionnaire() error = %v", err)
	}

	clone := CloneSessionQuestionnaire(questionnaire)
	*clone.Fields[2].MaxLength = 1
	clone.Fields[0].Options[0].Label = "changed"
	if *questionnaire.Fields[2].MaxLength == 1 ||
		questionnaire.Fields[0].Options[0].Label == "changed" {
		t.Fatal("CloneSessionQuestionnaire() shared mutable field state")
	}
}

func TestValidateSessionQuestionnaireRejectsInvalidContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*SessionQuestionnaire)
	}{
		{
			name: "missing identity",
			mutate: func(value *SessionQuestionnaire) {
				value.QuestionnaireVersionID = uuid.Nil
			},
		},
		{
			name: "empty fields",
			mutate: func(value *SessionQuestionnaire) {
				value.Fields = nil
			},
		},
		{
			name: "duplicate code",
			mutate: func(value *SessionQuestionnaire) {
				value.Fields[1].Code = value.Fields[0].Code
			},
		},
		{
			name: "unordered fields",
			mutate: func(value *SessionQuestionnaire) {
				value.Fields[1].SortOrder = value.Fields[0].SortOrder
			},
		},
		{
			name: "multiple choice requires bounded selection",
			mutate: func(value *SessionQuestionnaire) {
				value.Fields[1].MaxSelections = nil
			},
		},
		{
			name: "text requires explicit maximum",
			mutate: func(value *SessionQuestionnaire) {
				value.Fields[2].MaxLength = nil
			},
		},
		{
			name: "choice rejects text limits",
			mutate: func(value *SessionQuestionnaire) {
				value.Fields[0].MaxLength = questionnaireIntPointer(10)
			},
		},
		{
			name: "duplicate option",
			mutate: func(value *SessionQuestionnaire) {
				value.Fields[1].Options[1].Code =
					value.Fields[1].Options[0].Code
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			questionnaire := validSessionQuestionnaireFixture()
			test.mutate(&questionnaire)
			err := ValidateSessionQuestionnaire(questionnaire)
			if !errors.Is(err, ErrInvalidQuestionnaire) {
				t.Fatalf("ValidateSessionQuestionnaire() error = %v", err)
			}
		})
	}
}

func TestNormalizeQuestionnaireAnswersReturnsStableCompleteSnapshot(
	t *testing.T,
) {
	t.Parallel()

	questionnaire := validSessionQuestionnaireFixture()
	answers, err := NormalizeQuestionnaireAnswers(
		questionnaire,
		[]QuestionnaireAnswer{
			{
				FieldID: questionnaire.Fields[1].FieldID,
				Values:  []string{"product", "agents"},
			},
			{
				FieldID: questionnaire.Fields[0].FieldID,
				Values:  []string{"beginner"},
			},
		},
	)
	if err != nil {
		t.Fatalf("NormalizeQuestionnaireAnswers() error = %v", err)
	}
	if len(answers) != len(questionnaire.Fields) ||
		answers[0].FieldID != questionnaire.Fields[0].FieldID ||
		len(answers[1].Values) != 2 || answers[1].Values[0] != "agents" ||
		answers[1].Values[1] != "product" || answers[2].Values == nil ||
		len(answers[2].Values) != 0 {
		t.Fatalf("NormalizeQuestionnaireAnswers() = %+v", answers)
	}
	answers[1].Values[0] = "changed"
	if questionnaire.Fields[1].Options[0].Code == "changed" {
		t.Fatal("normalized answers share questionnaire state")
	}
}

func TestNormalizeQuestionnaireAnswersRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	questionnaire := validSessionQuestionnaireFixture()
	tests := []struct {
		name    string
		answers []QuestionnaireAnswer
	}{
		{name: "required omitted"},
		{
			name: "unknown field",
			answers: []QuestionnaireAnswer{{
				FieldID: uuid.New(), Values: []string{"beginner"},
			}},
		},
		{
			name: "unknown choice",
			answers: []QuestionnaireAnswer{{
				FieldID: questionnaire.Fields[0].FieldID,
				Values:  []string{"expert"},
			}},
		},
		{
			name: "duplicate answer",
			answers: []QuestionnaireAnswer{
				{
					FieldID: questionnaire.Fields[0].FieldID,
					Values:  []string{"beginner"},
				},
				{
					FieldID: questionnaire.Fields[0].FieldID,
					Values:  []string{"experienced"},
				},
			},
		},
		{
			name: "multiple choice exceeds maximum",
			answers: []QuestionnaireAnswer{
				{
					FieldID: questionnaire.Fields[0].FieldID,
					Values:  []string{"beginner"},
				},
				{
					FieldID: questionnaire.Fields[1].FieldID,
					Values:  []string{"agents", "product", "agents"},
				},
			},
		},
		{
			name: "single line newline",
			answers: []QuestionnaireAnswer{
				{
					FieldID: questionnaire.Fields[0].FieldID,
					Values:  []string{"beginner"},
				},
				{
					FieldID: questionnaire.Fields[2].FieldID,
					Values:  []string{"two\nlines"},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NormalizeQuestionnaireAnswers(questionnaire, test.answers)
			if !errors.Is(err, ErrInvalidQuestionnaireAnswers) {
				t.Fatalf("NormalizeQuestionnaireAnswers() error = %v", err)
			}
		})
	}
}

func validSessionQuestionnaireFixture() SessionQuestionnaire {
	return SessionQuestionnaire{
		QuestionnaireVersionID: uuid.New(),
		InstanceID:             uuid.New(),
		SessionID:              uuid.New(),
		Version:                3,
		PrivacyPurpose:         "用于活动报名与现场服务",
		PrivacyPolicyVersion:   "privacy-2026-09",
		PublishedAt:            time.Date(2026, 9, 13, 1, 0, 0, 0, time.UTC),
		Fields: []QuestionnaireField{
			{
				FieldID:   uuid.New(),
				Code:      "experience_level",
				Type:      QuestionnaireFieldSingleChoice,
				Label:     "AI 经验",
				Required:  true,
				SortOrder: 0,
				Options: []QuestionnaireOption{
					{Code: "beginner", Label: "刚开始"},
					{Code: "experienced", Label: "有经验"},
				},
			},
			{
				FieldID:       uuid.New(),
				Code:          "topics",
				Type:          QuestionnaireFieldMultipleChoice,
				Label:         "感兴趣的话题",
				HelpText:      "最多选择两项",
				SortOrder:     10,
				MaxSelections: questionnaireIntPointer(2),
				Options: []QuestionnaireOption{
					{Code: "agents", Label: "智能体"},
					{Code: "product", Label: "产品"},
				},
			},
			{
				FieldID:   uuid.New(),
				Code:      "expectation",
				Type:      QuestionnaireFieldSingleLine,
				Label:     "期待收获",
				SortOrder: 20,
				MinLength: questionnaireIntPointer(2),
				MaxLength: questionnaireIntPointer(100),
				Options:   []QuestionnaireOption{},
			},
			{
				FieldID:   uuid.New(),
				Code:      "notes",
				Type:      QuestionnaireFieldMultiline,
				Label:     "补充说明",
				SortOrder: 30,
				MaxLength: questionnaireIntPointer(1000),
				Options:   []QuestionnaireOption{},
			},
			{
				FieldID:   uuid.New(),
				Code:      "area",
				Type:      QuestionnaireFieldArea,
				Label:     "所在区域",
				SortOrder: 40,
				Options: []QuestionnaireOption{
					{Code: "heping", Label: "和平区"},
				},
			},
		},
	}
}

func questionnaireIntPointer(value int) *int {
	return &value
}
