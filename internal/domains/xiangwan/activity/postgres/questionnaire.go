package activitypostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func (repository *Repository) ReadSessionQuestionnaire(
	ctx context.Context,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
) (activity.SessionQuestionnaire, error) {
	rows, err := repository.db.queryContext(ctx, `
SELECT
    questionnaire.questionnaire_version_id,
    questionnaire.instance_id,
    activity_session.id,
    questionnaire.version,
    questionnaire.privacy_purpose,
    questionnaire.privacy_policy_version,
    questionnaire.published_at,
    field.field_id,
    field.field_code,
    field.field_type,
    field.label,
    field.help_text,
    field.is_required,
    field.sort_order,
    field.min_length,
    field.max_length,
    field.max_selections,
    field.options
FROM xiangwan_activity_sessions AS activity_session
JOIN xiangwan_activity_instances AS activity_instance
  ON activity_instance.tenant_id = activity_session.tenant_id
 AND activity_instance.id = activity_session.instance_id
JOIN xiangwan_activity_series AS activity_series
  ON activity_series.tenant_id = activity_instance.tenant_id
 AND activity_series.id = activity_instance.series_id
JOIN xiangwan_brand_profiles AS brand_profile
  ON brand_profile.tenant_id = activity_series.tenant_id
JOIN LATERAL (
    SELECT assignment.questionnaire_version_id
    FROM xiangwan_instance_questionnaires AS assignment
    WHERE assignment.tenant_id = activity_instance.tenant_id
      AND assignment.instance_id = activity_instance.id
    ORDER BY assignment.assignment_version DESC
    LIMIT 1
) AS current_assignment ON TRUE
JOIN xiangwan_questionnaire_versions AS questionnaire
  ON questionnaire.tenant_id = activity_instance.tenant_id
 AND questionnaire.instance_id = activity_instance.id
 AND questionnaire.questionnaire_version_id =
     current_assignment.questionnaire_version_id
 AND questionnaire.status = 'published'
JOIN xiangwan_questionnaire_fields AS field
  ON field.tenant_id = questionnaire.tenant_id
 AND field.instance_id = questionnaire.instance_id
 AND field.questionnaire_version_id = questionnaire.questionnaire_version_id
WHERE activity_session.tenant_id = $1
  AND activity_session.id = $2
  AND activity_series.status = 'active'
  AND activity_series.current_public_instance_id = activity_instance.id
  AND activity_instance.status = 'published'
  AND activity_session.status = 'published'
  AND brand_profile.lifecycle_status = 'active'
ORDER BY field.sort_order, field.field_id
`, tenantID, sessionID)
	if err != nil {
		return activity.SessionQuestionnaire{}, fmt.Errorf(
			"query xiangwan Session questionnaire: %w",
			err,
		)
	}
	defer func() { _ = rows.Close() }()

	var questionnaire activity.SessionQuestionnaire
	for rows.Next() {
		field, identity, scanErr := scanQuestionnaireRow(rows)
		if scanErr != nil {
			return activity.SessionQuestionnaire{}, fmt.Errorf(
				"scan xiangwan Session questionnaire: %w",
				scanErr,
			)
		}
		if questionnaire.QuestionnaireVersionID == uuid.Nil {
			questionnaire = identity
		} else if questionnaire.QuestionnaireVersionID !=
			identity.QuestionnaireVersionID ||
			questionnaire.InstanceID != identity.InstanceID ||
			questionnaire.SessionID != identity.SessionID ||
			questionnaire.Version != identity.Version {
			return activity.SessionQuestionnaire{}, activity.ErrInvalidQuestionnaire
		}
		questionnaire.Fields = append(questionnaire.Fields, field)
	}
	if err := rows.Err(); err != nil {
		return activity.SessionQuestionnaire{}, fmt.Errorf(
			"iterate xiangwan Session questionnaire: %w",
			err,
		)
	}
	if questionnaire.QuestionnaireVersionID == uuid.Nil {
		return activity.SessionQuestionnaire{}, activity.ErrQuestionnaireUnavailable
	}
	if err := activity.ValidateSessionQuestionnaire(questionnaire); err != nil {
		return activity.SessionQuestionnaire{}, fmt.Errorf(
			"validate xiangwan Session questionnaire: %w",
			err,
		)
	}
	return activity.CloneSessionQuestionnaire(questionnaire), nil
}

func scanQuestionnaireRow(
	row rowScanner,
) (
	activity.QuestionnaireField,
	activity.SessionQuestionnaire,
	error,
) {
	var questionnaire activity.SessionQuestionnaire
	var field activity.QuestionnaireField
	var minLength sql.NullInt64
	var maxLength sql.NullInt64
	var maxSelections sql.NullInt64
	var optionsJSON []byte
	err := row.Scan(
		&questionnaire.QuestionnaireVersionID,
		&questionnaire.InstanceID,
		&questionnaire.SessionID,
		&questionnaire.Version,
		&questionnaire.PrivacyPurpose,
		&questionnaire.PrivacyPolicyVersion,
		&questionnaire.PublishedAt,
		&field.FieldID,
		&field.Code,
		&field.Type,
		&field.Label,
		&field.HelpText,
		&field.Required,
		&field.SortOrder,
		&minLength,
		&maxLength,
		&maxSelections,
		&optionsJSON,
	)
	if err != nil {
		return activity.QuestionnaireField{},
			activity.SessionQuestionnaire{}, err
	}
	field.MinLength, err = questionnaireNullableInt(minLength)
	if err != nil {
		return activity.QuestionnaireField{},
			activity.SessionQuestionnaire{}, err
	}
	field.MaxLength, err = questionnaireNullableInt(maxLength)
	if err != nil {
		return activity.QuestionnaireField{},
			activity.SessionQuestionnaire{}, err
	}
	field.MaxSelections, err = questionnaireNullableInt(maxSelections)
	if err != nil {
		return activity.QuestionnaireField{},
			activity.SessionQuestionnaire{}, err
	}
	if err := json.Unmarshal(optionsJSON, &field.Options); err != nil {
		return activity.QuestionnaireField{},
			activity.SessionQuestionnaire{}, fmt.Errorf(
				"decode questionnaire options: %w",
				err,
			)
	}
	if field.Options == nil {
		field.Options = []activity.QuestionnaireOption{}
	}
	return field, questionnaire, nil
}

func questionnaireNullableInt(value sql.NullInt64) (*int, error) {
	if !value.Valid {
		return nil, nil
	}
	converted := int(value.Int64)
	if int64(converted) != value.Int64 {
		return nil, errors.New("questionnaire integer is outside platform range")
	}
	return &converted, nil
}
