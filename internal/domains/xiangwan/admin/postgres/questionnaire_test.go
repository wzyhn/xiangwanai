package postgres

import (
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestNormalizeQuestionnaireFieldsCanonicalizesOperatorInput(t *testing.T) {
	t.Parallel()
	fields := []activity.QuestionnaireField{
		{
			FieldID: uuid.New(), Code: " role ", Label: " 角色 ", HelpText: " 提示 ",
			SortOrder: 99, Options: []activity.QuestionnaireOption{{Code: " option ", Label: " 选项 "}},
		},
		{Code: "note", Label: "备注", SortOrder: 1},
	}
	got := normalizeQuestionnaireFields(fields)
	if len(got) != 2 || got[0].Code != "role" || got[0].Label != "角色" ||
		got[0].HelpText != "提示" || got[0].SortOrder != 0 ||
		got[0].Options[0].Code != "option" || got[0].Options[0].Label != "选项" ||
		got[1].SortOrder != 1 {
		t.Fatalf("normalizeQuestionnaireFields() = %+v", got)
	}
	if fields[0].Code != " role " || fields[0].Options[0].Code != " option " {
		t.Fatal("normalizeQuestionnaireFields mutated the caller's fields")
	}
}

func TestQuestionnairePolicyVersionPatternMatchesPublishedVersionShape(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"privacy-v1", "wechat.2026:09"} {
		if !questionnairePolicyVersionPattern.MatchString(value) {
			t.Fatalf("policy version %q was rejected", value)
		}
	}
	for _, value := range []string{"", " privacy-v1", "-privacy-v1", "privacy/v1"} {
		if questionnairePolicyVersionPattern.MatchString(value) {
			t.Fatalf("policy version %q was accepted", value)
		}
	}
}

func TestPrepareTemplateFieldsAssignsStableShapeWithoutMutatingInput(t *testing.T) {
	t.Parallel()
	maxSelections := 2
	input := []activity.QuestionnaireField{{
		Code: " interest ", Type: activity.QuestionnaireFieldMultipleChoice,
		Label: "兴趣", HelpText: " 选择两项以内 ", Required: true,
		SortOrder: 99, MaxSelections: &maxSelections,
		Options: []activity.QuestionnaireOption{{Code: " ai ", Label: "人工智能"}, {Code: " art ", Label: "艺术"}},
	}}
	got, err := prepareTemplateFields(input, "用于活动报名", "privacy-v1")
	if err != nil {
		t.Fatalf("prepareTemplateFields() error = %v", err)
	}
	if len(got) != 1 || got[0].FieldID == uuid.Nil || got[0].SortOrder != 0 ||
		got[0].Code != "interest" || got[0].HelpText != "选择两项以内" ||
		got[0].Options[0].Code != "ai" || got[0].Options[1].Code != "art" {
		t.Fatalf("prepareTemplateFields() = %+v", got)
	}
	if input[0].Code != " interest " || input[0].Options[0].Code != " ai " {
		t.Fatal("prepareTemplateFields mutated input")
	}
}

func TestQuestionnaireTemplateFieldRecordsRoundTrip(t *testing.T) {
	t.Parallel()
	maxLength := 80
	fields := []activity.QuestionnaireField{{
		FieldID: uuid.New(), Code: "role", Type: activity.QuestionnaireFieldSingleLine,
		Label: "角色", Required: true, SortOrder: 0, MaxLength: &maxLength,
	}}
	records := templateFieldRecords(fields)
	cloned := templateFieldsFromRecords(records)
	if len(cloned) != 1 || cloned[0].FieldID != fields[0].FieldID || cloned[0].Code != "role" ||
		cloned[0].MaxLength == nil || *cloned[0].MaxLength != 80 {
		t.Fatalf("template field round trip = %+v", cloned)
	}
	*cloned[0].MaxLength = 20
	if *fields[0].MaxLength != 80 {
		t.Fatal("template field round trip shared pointer state")
	}
}
