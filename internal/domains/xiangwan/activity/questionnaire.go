package activity

import (
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type QuestionnaireFieldType string

const (
	QuestionnaireFieldSingleChoice   QuestionnaireFieldType = "single_choice"
	QuestionnaireFieldMultipleChoice QuestionnaireFieldType = "multiple_choice"
	QuestionnaireFieldSingleLine     QuestionnaireFieldType = "single_line"
	QuestionnaireFieldMultiline      QuestionnaireFieldType = "multiline"
	QuestionnaireFieldArea           QuestionnaireFieldType = "area"
)

const (
	MaxQuestionnaireFields  = 100
	MaxQuestionnaireOptions = 100
)

var questionnaireCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type QuestionnaireOption struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

type QuestionnaireField struct {
	FieldID       uuid.UUID
	Code          string
	Type          QuestionnaireFieldType
	Label         string
	HelpText      string
	Required      bool
	SortOrder     int
	MinLength     *int
	MaxLength     *int
	MaxSelections *int
	Options       []QuestionnaireOption
}

type QuestionnaireAnswer struct {
	FieldID uuid.UUID
	Values  []string
}

type SessionQuestionnaire struct {
	QuestionnaireVersionID uuid.UUID
	InstanceID             uuid.UUID
	SessionID              uuid.UUID
	Version                int64
	PrivacyPurpose         string
	PrivacyPolicyVersion   string
	PublishedAt            time.Time
	Fields                 []QuestionnaireField
}

var (
	ErrQuestionnaireUnavailable    = errors.New("xiangwan questionnaire unavailable")
	ErrInvalidQuestionnaire        = errors.New("invalid xiangwan questionnaire")
	ErrInvalidQuestionnaireAnswers = errors.New(
		"invalid xiangwan questionnaire answers",
	)
)

type InvalidQuestionnaireError struct {
	Violations []FactViolation
}

func (err *InvalidQuestionnaireError) Error() string {
	if err == nil || len(err.Violations) == 0 {
		return ErrInvalidQuestionnaire.Error()
	}
	return ErrInvalidQuestionnaire.Error() + ": " +
		strings.Join(publicationViolationStrings(err.Violations), ", ")
}

func (*InvalidQuestionnaireError) Unwrap() error {
	return ErrInvalidQuestionnaire
}

type InvalidQuestionnaireAnswersError struct {
	Violations []FactViolation
}

func (err *InvalidQuestionnaireAnswersError) Error() string {
	if err == nil || len(err.Violations) == 0 {
		return ErrInvalidQuestionnaireAnswers.Error()
	}
	return ErrInvalidQuestionnaireAnswers.Error() + ": " +
		strings.Join(publicationViolationStrings(err.Violations), ", ")
}

func (*InvalidQuestionnaireAnswersError) Unwrap() error {
	return ErrInvalidQuestionnaireAnswers
}

func ValidateSessionQuestionnaire(questionnaire SessionQuestionnaire) error {
	violations := questionnaireViolations(questionnaire)
	if len(violations) > 0 {
		return &InvalidQuestionnaireError{Violations: violations}
	}
	return nil
}

func CloneSessionQuestionnaire(
	questionnaire SessionQuestionnaire,
) SessionQuestionnaire {
	clone := questionnaire
	clone.Fields = make([]QuestionnaireField, len(questionnaire.Fields))
	for index, field := range questionnaire.Fields {
		clone.Fields[index] = field
		clone.Fields[index].MinLength = cloneQuestionnaireInt(field.MinLength)
		clone.Fields[index].MaxLength = cloneQuestionnaireInt(field.MaxLength)
		clone.Fields[index].MaxSelections = cloneQuestionnaireInt(
			field.MaxSelections,
		)
		clone.Fields[index].Options = append(
			[]QuestionnaireOption(nil),
			field.Options...,
		)
	}
	return clone
}

// NormalizeQuestionnaireAnswers validates the complete submission against one
// exact immutable questionnaire version. It returns one answer per published
// field in field order; optional omissions become empty arrays and multiple
// choice values are sorted so operation fingerprints are stable.
func NormalizeQuestionnaireAnswers(
	questionnaire SessionQuestionnaire,
	answers []QuestionnaireAnswer,
) ([]QuestionnaireAnswer, error) {
	if err := ValidateSessionQuestionnaire(questionnaire); err != nil {
		return nil, err
	}
	violations := make([]FactViolation, 0)
	if len(answers) > len(questionnaire.Fields) {
		violations = append(violations, FactViolation{
			Field: "answers", Code: ViolationOutOfRange,
		})
	}
	byField := make(map[uuid.UUID][]string, len(answers))
	knownFields := make(map[uuid.UUID]struct{}, len(questionnaire.Fields))
	for _, field := range questionnaire.Fields {
		knownFields[field.FieldID] = struct{}{}
	}
	for index, answer := range answers {
		prefix := "answers[" + strconv.Itoa(index) + "]."
		if answer.FieldID == uuid.Nil {
			violations = append(violations, FactViolation{
				Field: prefix + "field_id", Code: ViolationRequired,
			})
			continue
		}
		if _, known := knownFields[answer.FieldID]; !known {
			violations = append(violations, FactViolation{
				Field: prefix + "field_id", Code: ViolationInvalidChoice,
			})
			continue
		}
		if _, duplicate := byField[answer.FieldID]; duplicate {
			violations = append(violations, FactViolation{
				Field: prefix + "field_id", Code: ViolationDuplicate,
			})
			continue
		}
		byField[answer.FieldID] = append([]string(nil), answer.Values...)
	}

	normalized := make([]QuestionnaireAnswer, 0, len(questionnaire.Fields))
	for index, field := range questionnaire.Fields {
		values := append([]string(nil), byField[field.FieldID]...)
		fieldName := "fields[" + strconv.Itoa(index) + "].answer"
		violations = append(
			violations,
			questionnaireAnswerViolations(field, values, fieldName)...,
		)
		if field.Type == QuestionnaireFieldMultipleChoice {
			sort.Strings(values)
		}
		if values == nil {
			values = []string{}
		}
		normalized = append(normalized, QuestionnaireAnswer{
			FieldID: field.FieldID,
			Values:  values,
		})
	}
	if len(violations) > 0 {
		return nil, &InvalidQuestionnaireAnswersError{
			Violations: violations,
		}
	}
	return normalized, nil
}

func questionnaireAnswerViolations(
	field QuestionnaireField,
	values []string,
	fieldName string,
) []FactViolation {
	violations := make([]FactViolation, 0)
	if field.Required && len(values) == 0 {
		return append(violations, FactViolation{
			Field: fieldName, Code: ViolationRequired,
		})
	}
	switch field.Type {
	case QuestionnaireFieldSingleChoice, QuestionnaireFieldArea:
		if len(values) > 1 {
			violations = append(violations, FactViolation{
				Field: fieldName, Code: ViolationOutOfRange,
			})
		}
		violations = append(
			violations,
			questionnaireChoiceAnswerViolations(field, values, fieldName)...,
		)
	case QuestionnaireFieldMultipleChoice:
		if field.MaxSelections == nil || len(values) > *field.MaxSelections {
			violations = append(violations, FactViolation{
				Field: fieldName, Code: ViolationOutOfRange,
			})
		}
		violations = append(
			violations,
			questionnaireChoiceAnswerViolations(field, values, fieldName)...,
		)
	case QuestionnaireFieldSingleLine, QuestionnaireFieldMultiline:
		if len(values) > 1 {
			violations = append(violations, FactViolation{
				Field: fieldName, Code: ViolationOutOfRange,
			})
		}
		if len(values) == 1 {
			minimum := 0
			if field.MinLength != nil {
				minimum = *field.MinLength
			}
			length := len([]rune(values[0]))
			if strings.TrimSpace(values[0]) == "" ||
				field.MaxLength == nil || length < minimum ||
				length > *field.MaxLength ||
				(field.Type == QuestionnaireFieldSingleLine &&
					strings.ContainsAny(values[0], "\r\n")) {
				violations = append(violations, FactViolation{
					Field: fieldName, Code: ViolationOutOfRange,
				})
			}
		}
	default:
		violations = append(violations, FactViolation{
			Field: fieldName, Code: ViolationInvalidChoice,
		})
	}
	return violations
}

func questionnaireChoiceAnswerViolations(
	field QuestionnaireField,
	values []string,
	fieldName string,
) []FactViolation {
	allowed := make(map[string]struct{}, len(field.Options))
	for _, option := range field.Options {
		allowed[option.Code] = struct{}{}
	}
	seen := make(map[string]struct{}, len(values))
	violations := make([]FactViolation, 0)
	for _, value := range values {
		if _, exists := allowed[value]; !exists {
			violations = append(violations, FactViolation{
				Field: fieldName, Code: ViolationInvalidChoice,
			})
		} else if _, duplicate := seen[value]; duplicate {
			violations = append(violations, FactViolation{
				Field: fieldName, Code: ViolationDuplicate,
			})
		} else {
			seen[value] = struct{}{}
		}
	}
	return violations
}

func questionnaireViolations(
	questionnaire SessionQuestionnaire,
) []FactViolation {
	violations := make([]FactViolation, 0)
	if questionnaire.QuestionnaireVersionID == uuid.Nil {
		violations = append(violations, FactViolation{
			Field: "questionnaire_version_id", Code: ViolationRequired,
		})
	}
	if questionnaire.InstanceID == uuid.Nil {
		violations = append(violations, FactViolation{
			Field: "instance_id", Code: ViolationRequired,
		})
	}
	if questionnaire.SessionID == uuid.Nil {
		violations = append(violations, FactViolation{
			Field: "session_id", Code: ViolationRequired,
		})
	}
	if questionnaire.Version < 1 {
		violations = append(violations, FactViolation{
			Field: "version", Code: ViolationMustBePositive,
		})
	}
	if strings.TrimSpace(questionnaire.PrivacyPurpose) == "" ||
		strings.TrimSpace(questionnaire.PrivacyPurpose) !=
			questionnaire.PrivacyPurpose ||
		len([]rune(questionnaire.PrivacyPurpose)) > 500 {
		violations = append(violations, FactViolation{
			Field: "privacy_purpose", Code: ViolationOutOfRange,
		})
	}
	if strings.TrimSpace(questionnaire.PrivacyPolicyVersion) == "" ||
		strings.TrimSpace(questionnaire.PrivacyPolicyVersion) !=
			questionnaire.PrivacyPolicyVersion ||
		len([]rune(questionnaire.PrivacyPolicyVersion)) > 100 {
		violations = append(violations, FactViolation{
			Field: "privacy_policy_version", Code: ViolationOutOfRange,
		})
	}
	if questionnaire.PublishedAt.IsZero() {
		violations = append(violations, FactViolation{
			Field: "published_at", Code: ViolationRequired,
		})
	}
	if len(questionnaire.Fields) == 0 ||
		len(questionnaire.Fields) > MaxQuestionnaireFields {
		violations = append(violations, FactViolation{
			Field: "fields", Code: ViolationOutOfRange,
		})
	}

	seenIDs := make(map[uuid.UUID]struct{}, len(questionnaire.Fields))
	seenCodes := make(map[string]struct{}, len(questionnaire.Fields))
	previousSortOrder := -1
	for index, field := range questionnaire.Fields {
		prefix := "fields[" + strconv.Itoa(index) + "]."
		violations = append(
			violations,
			questionnaireFieldViolations(
				field,
				prefix,
				previousSortOrder,
				seenIDs,
				seenCodes,
			)...,
		)
		previousSortOrder = field.SortOrder
	}
	return violations
}

func questionnaireFieldViolations(
	field QuestionnaireField,
	prefix string,
	previousSortOrder int,
	seenIDs map[uuid.UUID]struct{},
	seenCodes map[string]struct{},
) []FactViolation {
	violations := make([]FactViolation, 0)
	if field.FieldID == uuid.Nil {
		violations = append(violations, FactViolation{
			Field: prefix + "field_id", Code: ViolationRequired,
		})
	} else if _, duplicate := seenIDs[field.FieldID]; duplicate {
		violations = append(violations, FactViolation{
			Field: prefix + "field_id", Code: ViolationDuplicate,
		})
	} else {
		seenIDs[field.FieldID] = struct{}{}
	}
	if !questionnaireCodePattern.MatchString(field.Code) {
		violations = append(violations, FactViolation{
			Field: prefix + "code", Code: ViolationInvalidChoice,
		})
	} else if _, duplicate := seenCodes[field.Code]; duplicate {
		violations = append(violations, FactViolation{
			Field: prefix + "code", Code: ViolationDuplicate,
		})
	} else {
		seenCodes[field.Code] = struct{}{}
	}
	if !knownQuestionnaireFieldType(field.Type) {
		violations = append(violations, FactViolation{
			Field: prefix + "type", Code: ViolationInvalidChoice,
		})
	}
	if strings.TrimSpace(field.Label) == "" ||
		strings.TrimSpace(field.Label) != field.Label ||
		len([]rune(field.Label)) > 200 {
		violations = append(violations, FactViolation{
			Field: prefix + "label", Code: ViolationOutOfRange,
		})
	}
	if strings.TrimSpace(field.HelpText) != field.HelpText ||
		len([]rune(field.HelpText)) > 500 {
		violations = append(violations, FactViolation{
			Field: prefix + "help_text", Code: ViolationOutOfRange,
		})
	}
	if field.SortOrder < 0 || field.SortOrder <= previousSortOrder {
		violations = append(violations, FactViolation{
			Field: prefix + "sort_order", Code: ViolationOutOfRange,
		})
	}
	violations = append(
		violations,
		questionnaireFieldShapeViolations(field, prefix)...,
	)
	return violations
}

func questionnaireFieldShapeViolations(
	field QuestionnaireField,
	prefix string,
) []FactViolation {
	violations := make([]FactViolation, 0)
	choiceField := field.Type == QuestionnaireFieldSingleChoice ||
		field.Type == QuestionnaireFieldMultipleChoice ||
		field.Type == QuestionnaireFieldArea
	if choiceField {
		if len(field.Options) == 0 ||
			len(field.Options) > MaxQuestionnaireOptions {
			violations = append(violations, FactViolation{
				Field: prefix + "options", Code: ViolationOutOfRange,
			})
		}
		if field.MinLength != nil || field.MaxLength != nil {
			violations = append(violations, FactViolation{
				Field: prefix + "length", Code: ViolationInvalidChoice,
			})
		}
	} else if len(field.Options) != 0 {
		violations = append(violations, FactViolation{
			Field: prefix + "options", Code: ViolationInvalidChoice,
		})
	}

	seenOptions := make(map[string]struct{}, len(field.Options))
	for index, option := range field.Options {
		optionPrefix := prefix + "options[" + strconv.Itoa(index) + "]."
		if !questionnaireCodePattern.MatchString(option.Code) {
			violations = append(violations, FactViolation{
				Field: optionPrefix + "code", Code: ViolationInvalidChoice,
			})
		} else if _, duplicate := seenOptions[option.Code]; duplicate {
			violations = append(violations, FactViolation{
				Field: optionPrefix + "code", Code: ViolationDuplicate,
			})
		} else {
			seenOptions[option.Code] = struct{}{}
		}
		if strings.TrimSpace(option.Label) == "" ||
			strings.TrimSpace(option.Label) != option.Label ||
			len([]rune(option.Label)) > 100 {
			violations = append(violations, FactViolation{
				Field: optionPrefix + "label", Code: ViolationOutOfRange,
			})
		}
	}

	if field.Type == QuestionnaireFieldMultipleChoice {
		if field.MaxSelections == nil || *field.MaxSelections < 1 ||
			*field.MaxSelections > len(field.Options) {
			violations = append(violations, FactViolation{
				Field: prefix + "max_selections", Code: ViolationOutOfRange,
			})
		}
	} else if field.MaxSelections != nil {
		violations = append(violations, FactViolation{
			Field: prefix + "max_selections", Code: ViolationInvalidChoice,
		})
	}

	if field.Type == QuestionnaireFieldSingleLine ||
		field.Type == QuestionnaireFieldMultiline {
		maxAllowed := 2000
		if field.Type == QuestionnaireFieldSingleLine {
			maxAllowed = 200
		}
		if field.MaxLength == nil || *field.MaxLength < 1 ||
			*field.MaxLength > maxAllowed ||
			(field.MinLength != nil &&
				(*field.MinLength < 0 || *field.MinLength > *field.MaxLength)) {
			violations = append(violations, FactViolation{
				Field: prefix + "length", Code: ViolationOutOfRange,
			})
		}
	}
	return violations
}

func knownQuestionnaireFieldType(fieldType QuestionnaireFieldType) bool {
	switch fieldType {
	case QuestionnaireFieldSingleChoice,
		QuestionnaireFieldMultipleChoice,
		QuestionnaireFieldSingleLine,
		QuestionnaireFieldMultiline,
		QuestionnaireFieldArea:
		return true
	default:
		return false
	}
}

func cloneQuestionnaireInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
