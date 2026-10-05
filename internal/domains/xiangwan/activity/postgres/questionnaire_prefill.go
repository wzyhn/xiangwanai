package activitypostgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

// ReadQuestionnairePrefill maps an owner's immutable submission to the current
// field identities. Each Instance owns distinct questionnaire/field IDs; exact
// immutable definitions, purpose and privacy version must match across periods.
// No prior registration identity or contact information leaves this read.
func (repository *Repository) ReadQuestionnairePrefill(
	ctx context.Context, tenantID, principalID uuid.UUID,
	current activity.SessionQuestionnaire,
) ([]activity.QuestionnaireAnswer, error) {
	if ctx == nil || tenantID == uuid.Nil || principalID == uuid.Nil {
		return nil, activity.ErrInvalidQuestionnaire
	}
	if err := activity.ValidateSessionQuestionnaire(current); err != nil {
		return nil, err
	}
	rows, err := repository.db.queryContext(ctx, `
WITH current_definition AS (
    SELECT jsonb_agg(jsonb_build_array(
        field_code, field_type, label, help_text, is_required, sort_order,
        min_length, max_length, max_selections, options
    ) ORDER BY sort_order, field_code) AS definition
    FROM xiangwan_questionnaire_fields
    WHERE tenant_id = $1 AND questionnaire_version_id = $3
), previous_submission AS (
    SELECT snapshot.registration_id
    FROM xiangwan_registration_snapshots AS snapshot
    JOIN xiangwan_registrations AS registration
      ON registration.tenant_id = snapshot.tenant_id
     AND registration.id = snapshot.registration_id
     AND registration.principal_id = $2
    JOIN xiangwan_activity_instances AS current_instance
      ON current_instance.tenant_id = snapshot.tenant_id
     AND current_instance.id = $4
     AND current_instance.series_id = snapshot.series_id
    WHERE snapshot.tenant_id = $1 AND snapshot.principal_id = $2
      AND snapshot.questionnaire_privacy_purpose = $5
      AND snapshot.questionnaire_privacy_policy_version = $6
      AND EXISTS (
          SELECT 1 FROM principals
          WHERE id = $2 AND status = 'active' AND deleted_at IS NULL
      )
      AND NOT EXISTS (
          SELECT 1 FROM xiangwan_data_rights_cases AS deletion
          WHERE deletion.tenant_id = $1 AND deletion.principal_id = $2
            AND deletion.request_type = 'deletion'
            AND deletion.request_scope IN ('all_xiangwan_data', 'activity_participation')
            AND deletion.status <> 'rejected'
      )
      AND (
          SELECT jsonb_agg(jsonb_build_array(
              field_code, field_type, field_label, field_help_text, is_required, sort_order,
              min_length, max_length, max_selections, options
          ) ORDER BY sort_order, field_code)
          FROM xiangwan_registration_answers
          WHERE tenant_id = $1 AND registration_id = snapshot.registration_id
      ) = (SELECT definition FROM current_definition)
    ORDER BY snapshot.created_at DESC, snapshot.registration_id DESC
    LIMIT 1
)
SELECT current_field.field_id, previous_answer.answer_values
FROM previous_submission
JOIN xiangwan_registration_answers AS previous_answer
  ON previous_answer.tenant_id = $1
 AND previous_answer.registration_id = previous_submission.registration_id
JOIN xiangwan_questionnaire_fields AS current_field
  ON current_field.tenant_id = $1
 AND current_field.questionnaire_version_id = $3
 AND current_field.field_code = previous_answer.field_code
ORDER BY current_field.sort_order, current_field.field_id
`, tenantID, principalID, current.QuestionnaireVersionID, current.InstanceID,
		current.PrivacyPurpose, current.PrivacyPolicyVersion)
	if err != nil {
		return nil, fmt.Errorf("read owner questionnaire prefill: %w", err)
	}
	defer func() { _ = rows.Close() }()
	answers := make([]activity.QuestionnaireAnswer, 0)
	for rows.Next() {
		var answer activity.QuestionnaireAnswer
		var values []byte
		if err := rows.Scan(&answer.FieldID, &values); err != nil {
			return nil, fmt.Errorf("scan owner questionnaire prefill: %w", err)
		}
		if err := json.Unmarshal(values, &answer.Values); err != nil {
			return nil, fmt.Errorf("decode owner questionnaire prefill: %w", err)
		}
		answers = append(answers, answer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate owner questionnaire prefill: %w", err)
	}
	if len(answers) == 0 {
		return answers, nil
	}
	return activity.NormalizeQuestionnaireAnswers(current, answers)
}
